package aiaccessactions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/app/container"
	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	productadmin "github.com/dujiao-next/internal/modules/catalog/product/application/admin"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type productSnapshot struct{ item productdomain.Product }

func (s *productSnapshot) GetAdminByID(string) (*productdomain.Product, error) {
	copy := s.item
	return &copy, nil
}

type productWriterStub struct {
	item  *productSnapshot
	calls int
	fail  bool
}

func (w *productWriterStub) UpdateStatusIfUnchanged(_ string, at time.Time, active bool, price string, target bool) (*productdomain.Product, error) {
	w.calls++
	if w.fail || !w.item.item.UpdatedAt.Equal(at) || w.item.item.IsActive != active || w.item.item.PriceAmount.String() != price {
		return nil, productadmin.ErrAIProductChanged
	}
	w.item.item.IsActive = target
	w.item.item.UpdatedAt = w.item.item.UpdatedAt.Add(time.Minute)
	copy := w.item.item
	return &copy, nil
}
func actionHTTPFixture(t *testing.T) (*container.Container, *app.Actions, *domain.Key, *productSnapshot, *productWriterStub) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:action_approve_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&domain.Key{}, &domain.Audit{}, &domain.OAuthRefresh{}, &domain.RemoteConfig{}, &domain.ActionRequest{}, &domain.OrderCancellation{}, &domain.WalletRefundRequest{}); err != nil {
		t.Fatal(err)
	}
	store := gormstore.New(db)
	keys := app.New(store)
	actions := app.NewActions(store)
	remote := app.NewRemote(store, keys)
	if _, err := remote.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	key, _, err := keys.Create(context.Background(), "Codex", []string{app.ScopePublishRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	product := &productSnapshot{item: productdomain.Product{
		ID: 17, Slug: "demo", IsActive: false, CategoryID: 1,
		UpdatedAt:   time.Now().UTC().Truncate(time.Second),
		PriceAmount: money.FromDecimal(decimal.RequireFromString("29.90")),
	}}
	writer := &productWriterStub{item: product}
	return &container.Container{AiAccessService: keys, AiRemoteService: remote, AiActionService: actions}, actions, key, product, writer
}
func request(t *testing.T, container *container.Container, reader *productSnapshot, writer *productWriterStub, path string, withAdmin bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Admin{services: container, reader: reader, writer: writer}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if withAdmin {
			c.Set("admin_id", uint(7))
		}
		c.Next()
	})
	group := router.Group("/admin")
	group.GET("/ai-access/actions", h.List)
	group.POST("/ai-access/actions/:id/approve", h.Approve)
	group.POST("/ai-access/actions/:id/reject", h.Reject)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader("")))
	return w
}
func TestApprovalChangesProductOnlyOnceAfterHumanApproval(t *testing.T) {
	services, actions, key, reader, writer := actionHTTPFixture(t)
	req, err := actions.SubmitStatus(context.Background(), key, app.ProductStatusSnapshot{
		ProductID: 17, Title: "demo", CurrentActive: false, DesiredActive: true,
		Price: "29.90", UpdatedAt: reader.item.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/ai-access/actions/" + req.ID + "/approve"
	if got := request(t, services, reader, writer, path, false); got.Code != 403 {
		t.Fatalf("no-admin approval allowed %d", got.Code)
	}
	if writer.calls != 0 || reader.item.IsActive {
		t.Fatal("AI request modified product before approval")
	}
	if got := request(t, services, reader, writer, path, true); got.Code != 200 {
		t.Fatalf("approve HTTP=%d %s", got.Code, got.Body)
	}
	if writer.calls != 1 || !reader.item.IsActive {
		t.Fatal("approved status not applied exactly once")
	}
	if got := request(t, services, reader, writer, path, true); got.Code != 409 {
		t.Fatalf("replayed approve HTTP=%d", got.Code)
	}
	if writer.calls != 1 {
		t.Fatal("duplicate approval executed")
	}
	stored, err := actions.Owned(context.Background(), key, req.ID)
	if err != nil || stored.Status != domain.ActionSucceeded {
		t.Fatalf("not successful: %+v %v", stored, err)
	}
}
func TestApprovalCancelsStaleProductWithoutCallingWriter(t *testing.T) {
	services, actions, key, reader, writer := actionHTTPFixture(t)
	req, err := actions.SubmitStatus(context.Background(), key, app.ProductStatusSnapshot{
		ProductID: 17, Title: "demo", CurrentActive: false, DesiredActive: true,
		Price: "29.90", UpdatedAt: reader.item.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	reader.item.UpdatedAt = reader.item.UpdatedAt.Add(time.Minute)
	got := request(t, services, reader, writer, "/admin/ai-access/actions/"+req.ID+"/approve", true)
	if got.Code != 409 {
		t.Fatalf("stale product was approved HTTP %d", got.Code)
	}
	if writer.calls != 0 {
		t.Fatal("writer reached on stale approval")
	}
	stored, err := actions.Owned(context.Background(), key, req.ID)
	if err != nil || stored.Status != domain.ActionConflict {
		t.Fatalf("not conflicted: %+v %v", stored, err)
	}
}
func TestApprovalRequiresActiveKeyAndSupportsRejection(t *testing.T) {
	services, actions, key, reader, writer := actionHTTPFixture(t)
	req, err := actions.SubmitStatus(context.Background(), key, app.ProductStatusSnapshot{
		ProductID: 17, Title: "demo", CurrentActive: false, DesiredActive: true,
		Price: "29.90", UpdatedAt: reader.item.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = services.AiAccessService.Revoke(context.Background(), key.ID, 1); err != nil {
		t.Fatal(err)
	}
	got := request(t, services, reader, writer, "/admin/ai-access/actions/"+req.ID+"/approve", true)
	if got.Code != 409 || writer.calls != 0 {
		t.Fatalf("revoked applicant executed: %d calls=%d", got.Code, writer.calls)
	}
	got = request(t, services, reader, writer, "/admin/ai-access/actions/"+req.ID+"/reject", true)
	if got.Code != 200 {
		t.Fatalf("rejection failed HTTP %d", got.Code)
	}
	if got = request(t, services, reader, writer, "/admin/ai-access/actions/"+req.ID+"/reject", true); got.Code != 409 {
		t.Fatal("rejected request reusable")
	}
}
