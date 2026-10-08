package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	aihttp "github.com/dujiao-next/internal/modules/aiaccess/transport/http"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setup(t *testing.T) (*gorm.DB, *application.Service) {
	t.Helper()
	dsn := fmt.Sprintf("file:ai_keys_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&domain.Key{}, &domain.Audit{}, &domain.OAuthRefresh{}, &domain.ActionRequest{}, &domain.OrderReview{}, &domain.OrderCancellation{}, &domain.WalletRefundRequest{}); err != nil {
		t.Fatal(err)
	}
	return db, application.New(gormstore.New(db))
}
func TestTokenLifecycleAndAudit(t *testing.T) {
	db, svc := setup(t)
	ctx := context.Background()
	key, token, err := svc.Create(ctx, "NAS OpenClaw", []string{application.ScopeReport, application.ScopeCatalog}, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "djai_") || len(token) != 86 {
		t.Fatalf("bad token format: length %d", len(token))
	}
	var saved domain.Key
	if err := db.First(&saved, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", saved), token) || len(saved.TokenHash) != 64 {
		t.Fatal("raw token persisted or hash missing")
	}
	encoded, _ := json.Marshal(saved)
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), saved.TokenHash) {
		t.Fatal("secret leaked in serialized key")
	}
	if _, err = svc.Authenticate(ctx, token, application.ScopeCatalog); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authenticate(ctx, token, application.ScopeInventory); err == nil {
		t.Fatal("insufficient scope granted")
	}
	if _, err = svc.Authenticate(ctx, "djai_"+key.KeyID+"_"+strings.Repeat("a", 64), application.ScopeCatalog); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, err = svc.Authenticate(ctx, "Bearer "+token, application.ScopeCatalog); err == nil {
		t.Fatal("bearer header accepted as token")
	}
	rotated, err := svc.Rotate(ctx, key.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == token || !strings.HasPrefix(rotated, "djai_"+key.KeyID+"_") {
		t.Fatal("rotation did not retain ID and change secret")
	}
	if _, err = svc.Authenticate(ctx, token, application.ScopeCatalog); err == nil {
		t.Fatal("old token still valid after rotation")
	}
	if _, err = svc.Authenticate(ctx, rotated, application.ScopeReport); err != nil {
		t.Fatal(err)
	}
	if err = svc.Revoke(ctx, key.ID, 5); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authenticate(ctx, rotated, application.ScopeCatalog); err == nil {
		t.Fatal("revoked token still valid")
	}
	if err = svc.RecordUse(ctx, key, "/ai/products", "authorized"); err == nil {
		t.Fatal("revoked token recorded a new authorized use")
	}
	audit, err := svc.Audits(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 3 || audit[0].Action != "revoke" || audit[1].Action != "rotate" || audit[2].Action != "create" {
		t.Fatalf("audit lifecycle: %+v", audit)
	}
	content, _ := json.Marshal(audit)
	if strings.Contains(string(content), token) || strings.Contains(string(content), rotated) {
		t.Fatal("audit persisted token")
	}
}

func TestScopeValidationAndExpiry(t *testing.T) {
	db, svc := setup(t)
	ctx := context.Background()
	invalid := [][]string{{}, {application.ScopeCatalog, application.ScopeCatalog}, {"admin:*"}, {"card-secrets:read"}, {application.ScopeCatalog, "products:write"}}
	for _, scopes := range invalid {
		if _, _, err := svc.Create(ctx, "safe", scopes, 30, 1); err == nil {
			t.Fatalf("allowed scopes %v", scopes)
		}
	}
	for _, days := range []int{0, -1, 91} {
		if _, _, err := svc.Create(ctx, "safe", []string{application.ScopeCatalog}, days, 1); err == nil {
			t.Fatal("invalid expiry allowed")
		}
	}
	for _, name := range []string{"", "    ", "x\ninjected", strings.Repeat("中", 60)} {
		if _, _, err := svc.Create(ctx, name, []string{application.ScopeCatalog}, 30, 1); err == nil {
			t.Fatalf("invalid name allowed: %q", name)
		}
	}
	key, token, err := svc.Create(ctx, "OpenClaw", []string{application.ScopeInventory}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&domain.Key{}).Where("id=?", key.ID).UpdateColumn("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authenticate(ctx, token, application.ScopeInventory); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err = svc.Rotate(ctx, key.ID, 1); err == nil {
		t.Fatal("expired token rotated")
	}
}

func TestMachineRouteCannotUseAdminJWTAndEnforcesScopes(t *testing.T) {
	_, svc := setup(t)
	ctx := context.Background()
	_, catalog, err := svc.Create(ctx, "catalog", []string{application.ScopeCatalog}, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, inventory, err := svc.Create(ctx, "inventory", []string{application.ScopeInventory}, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ai/products", aihttp.MachineMiddleware(svc, application.ScopeCatalog, "/ai/products"), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": 1}) })
	request := func(authorization string) int {
		req := httptest.NewRequest("GET", "/ai/products", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request(""); got != 401 {
		t.Fatalf("no key HTTP=%d", got)
	}
	if got := request("Bearer JWT_ADMIN_TOKEN"); got != 401 {
		t.Fatalf("admin JWT HTTP=%d", got)
	}
	if got := request("Bearer " + inventory); got != 401 {
		t.Fatalf("wrong scope HTTP=%d", got)
	}
	if got := request("Bearer " + catalog); got != 200 {
		t.Fatalf("allowed HTTP=%d", got)
	}
	logs, err := svc.Audits(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range logs {
		if entry.Action == "access" && entry.Route == "/ai/products" && entry.Result == "authorized" {
			found = true
		}
	}
	if !found {
		t.Fatal("approved request missing audit")
	}
}

func TestLifecycleWriteAndAuditAreAtomic(t *testing.T) {
	db, svc := setup(t)
	ctx := context.Background()
	key, originalToken, err := svc.Create(ctx, "OpenClaw", []string{application.ScopeCatalog}, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Make only AI audit inserts fail; key writes must roll back entirely.
	err = db.Callback().Create().Before("gorm:create").Register("test:fail_ai_audit", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "ai_access_audit_logs" {
			tx.AddError(fmt.Errorf("simulated audit disk failure"))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:fail_ai_audit")

	if _, _, err = svc.Create(ctx, "Codex", []string{application.ScopeCatalog}, 10, 1); err == nil {
		t.Fatal("create should fail if audit fails")
	}
	var count int64
	if err = db.Model(&domain.Key{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("create left unaudited key: count=%d err=%v", count, err)
	}
	if _, err = svc.Rotate(ctx, key.ID, 1); err == nil {
		t.Fatal("rotation should fail if audit insert fails")
	}
	if _, err = svc.Authenticate(ctx, originalToken, application.ScopeCatalog); err != nil {
		t.Fatalf("rotation invalidated old token despite audit failure: %v", err)
	}
	if err = svc.Revoke(ctx, key.ID, 1); err == nil {
		t.Fatal("revoke should fail if audit insert fails")
	}
	if _, err = svc.Authenticate(ctx, originalToken, application.ScopeCatalog); err != nil {
		t.Fatalf("revoke invalidated token despite audit failure: %v", err)
	}
}

func TestAIAdminHandlersNeverReturnHashedToken(t *testing.T) {
	_, svc := setup(t)
	ctx := context.Background()
	key, secret, err := svc.Create(ctx, "Claude Code", []string{application.ScopeCatalog}, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	grp := router.Group("/admin")
	aihttp.RegisterAdminRoutes(grp, aihttp.NewAdminHandler(svc))
	req := httptest.NewRequest("GET", "/admin/ai-access/keys", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("list code=%d body=%s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), secret) || strings.Contains(rr.Body.String(), key.TokenHash) {
		t.Fatal("credential leaked in listing")
	}
	if !strings.Contains(rr.Body.String(), "Claude Code") {
		t.Fatal("public fields missing")
	}
}
