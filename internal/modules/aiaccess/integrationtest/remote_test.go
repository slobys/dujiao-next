package integrationtest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	remotehttp "github.com/dujiao-next/internal/modules/aiaccess/transport/http"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func remoteFixture(t *testing.T) (*gorm.DB, *app.Service, *app.RemoteService) {
	t.Helper()
	db, keys := setup(t)
	if err := db.AutoMigrate(&domain.RemoteConfig{}, &domain.OAuthClient{}, &domain.AuthorizationRequest{}, &domain.OAuthRefresh{}); err != nil {
		t.Fatal(err)
	}
	return db, keys, app.NewRemote(gormstore.New(db), keys)
}
func TestRemoteDisabledByDefaultAndStrictOrigin(t *testing.T) {
	_, _, remote := remoteFixture(t)
	ctx := context.Background()
	cfg, err := remote.GetConfig(ctx)
	if err != nil || cfg.Enabled {
		t.Fatalf("remote must default to disabled: %+v %v", cfg, err)
	}
	if _, err = remote.Active(ctx); !errors.Is(err, app.ErrRemoteDisabled) {
		t.Fatal("remote default not fail closed")
	}
	invalid := []string{
		"http://shop.example.com", "https://127.0.0.1", "https://localhost",
		"https://shop.example.com/path", "https://user:secret@shop.example.com",
		"https://shop.example.com?mcp=1", "https://shop.example.com/#fragment",
		"https://shop.example.com.evil\n", "https://.example.com", "https://-bad.example.com",
		"https://shop.example.com:99999", "https://shop.example.com.",
	}
	for _, v := range invalid {
		if _, err = remote.SetConfig(ctx, true, v); err == nil {
			t.Fatalf("enabled invalid origin %q", v)
		}
	}
	cfg, err = remote.SetConfig(ctx, true, "https://shop.example.com")
	if err != nil || !cfg.Enabled || cfg.PublicOrigin != "https://shop.example.com" {
		t.Fatalf("enable: %+v %v", cfg, err)
	}
	if _, err = remote.Active(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err = remote.SetConfig(ctx, false, "https://shop.example.com")
	if err != nil || cfg.Enabled {
		t.Fatal("remote disable failed")
	}
	if _, err = remote.Active(ctx); !errors.Is(err, app.ErrRemoteDisabled) {
		t.Fatal("disabled remote allowed")
	}
}
func TestOAuthBrowserPKCELifecycleAndRefresh(t *testing.T) {
	_, keys, remote := remoteFixture(t)
	ctx := context.Background()
	if _, err := remote.SetConfig(ctx, true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://evil.example/callback", "javascript:alert(1)", "file:///etc/passwd", "https://user:pw@bad.example/callback", "http://0.0.0.0:3333/cb"} {
		if _, err := remote.RegisterClient(ctx, "bad", []string{bad}); err == nil {
			t.Fatalf("accepted bad callback %q", bad)
		}
	}
	client, err := remote.RegisterClient(ctx, "Windows Codex", []string{"http://127.0.0.1:5000/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("A", 64)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	if _, err = remote.NewAuthorization(ctx, client.ClientID, "http://evil.example/callback", challenge, "state", "catalog:read", "https://shop.example.com/mcp"); err == nil {
		t.Fatal("unregistered redirect allowed")
	}
	if _, err = remote.NewAuthorization(ctx, client.ClientID, "http://127.0.0.1:5000/callback", challenge, "state", "products:write", "https://shop.example.com/mcp"); err == nil {
		t.Fatal("scope escalation allowed")
	}
	if _, err = remote.NewAuthorization(ctx, client.ClientID, "http://127.0.0.1:5000/callback", challenge, "state", "catalog:read", "https://evil.example/mcp"); err == nil {
		t.Fatal("cross-resource audience allowed")
	}
	pending, err := remote.NewAuthorization(ctx, client.ClientID, "http://127.0.0.1:5000/callback", challenge, "state-123", "catalog:read report:read", "https://shop.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = remote.Pending(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Approve(ctx, pending.ID, 9, []string{"inventory:read"}); err == nil {
		t.Fatal("approved unrequested permission")
	}
	redirect, err := remote.Approve(ctx, pending.ID, 9, []string{"report:read"})
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(redirect)
	if err != nil || uri.Host != "127.0.0.1:5000" || uri.Query().Get("state") != "state-123" || uri.Query().Get("iss") != "https://shop.example.com" {
		t.Fatalf("unsafe redirect %s %v", redirect, err)
	}
	code := uri.Query().Get("code")
	if _, err = remote.Exchange(ctx, client.ClientID, code, "http://127.0.0.1:5000/callback", strings.Repeat("B", 64), "https://shop.example.com/mcp"); err == nil {
		t.Fatal("bad PKCE redeemed code")
	}
	reply, err := remote.Exchange(ctx, client.ClientID, code, "http://127.0.0.1:5000/callback", verifier, "https://shop.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Scope != "report:read" || reply.ExpiresIn != 3600 || reply.RefreshToken == "" {
		t.Fatalf("bad OAuth token response %+v", reply)
	}
	if _, err = remote.Exchange(ctx, client.ClientID, code, "http://127.0.0.1:5000/callback", verifier, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("OAuth code replay accepted")
	}
	key, err := keys.AuthenticateMCP(ctx, reply.AccessToken, "https://shop.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keys.Authenticate(ctx, reply.AccessToken, app.ScopeReport); err == nil {
		t.Fatal("OAuth MCP token reused on admin-like REST API")
	}
	if _, err = keys.AuthenticateMCP(ctx, reply.AccessToken, "https://evil.example/mcp"); err == nil {
		t.Fatal("OAuth audience not enforced")
	}
	if app.HasScope(key.Scopes, app.ScopeCatalog) {
		t.Fatal("consent scope restriction failed")
	}
	next, err := remote.Refresh(ctx, client.ClientID, reply.RefreshToken, "https://shop.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if next.AccessToken == reply.AccessToken || next.RefreshToken == reply.RefreshToken {
		t.Fatal("refresh did not rotate both tokens")
	}
	if _, err = keys.AuthenticateMCP(ctx, reply.AccessToken, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("old access valid after refresh")
	}
	if _, err = remote.Refresh(ctx, client.ClientID, reply.RefreshToken, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("refresh replay accepted")
	}
	if _, err = keys.AuthenticateMCP(ctx, next.AccessToken, "https://shop.example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	manualRotated, err := keys.Rotate(ctx, key.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keys.AuthenticateMCP(ctx, next.AccessToken, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("manually rotated key still accepts old access")
	}
	if _, err = remote.Refresh(ctx, client.ClientID, next.RefreshToken, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("old refresh survived manual rotation")
	}
	if _, err = keys.AuthenticateMCP(ctx, manualRotated, "https://shop.example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	if err = keys.Revoke(ctx, key.ID, 9); err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Refresh(ctx, client.ClientID, next.RefreshToken, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("revoked credential refreshed")
	}
	if _, err = keys.AuthenticateMCP(ctx, next.AccessToken, "https://shop.example.com/mcp"); err == nil {
		t.Fatal("revoked OAuth credential accepted")
	}
}
func TestRemoteTransportFailsClosedAndMetadata(t *testing.T) {
	_, _, remote := remoteFixture(t)
	ctx := context.Background()
	handler := remotehttp.NewOAuthPublicHandler(remote, "/dj-secret-admin")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/.well-known/oauth-protected-resource/mcp", handler.ResourceMetadata)
	router.GET("/.well-known/oauth-authorization-server", handler.ServerMetadata)
	router.POST("/oauth/register", handler.Register)
	router.GET("/oauth/authorize", handler.Authorize)
	makeRequest := func(method, path, host, proto, remoteAddr, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = host
		r.RemoteAddr = remoteAddr
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	before := makeRequest("GET", "/.well-known/oauth-protected-resource/mcp", "shop.example.com", "https", "127.0.0.1:8123", "")
	if before.Code != 404 {
		t.Fatalf("disabled OAuth metadata HTTP=%d", before.Code)
	}
	if _, err := remote.SetConfig(ctx, true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	badHost := makeRequest("GET", "/.well-known/oauth-authorization-server", "attacker.example", "https", "127.0.0.1:8123", "")
	if badHost.Code != 421 {
		t.Fatalf("untrusted Host=%d", badHost.Code)
	}
	spoofed := makeRequest("GET", "/.well-known/oauth-authorization-server", "shop.example.com", "https", "203.0.113.2:8123", "")
	if spoofed.Code != 403 {
		t.Fatalf("untrusted public plaintext peer=%d", spoofed.Code)
	}
	valid := makeRequest("GET", "/.well-known/oauth-protected-resource/mcp", "shop.example.com", "https", "127.0.0.1:8123", "")
	if valid.Code != 200 {
		t.Fatalf("metadata=%d", valid.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(valid.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["resource"] != "https://shop.example.com/mcp" {
		t.Fatalf("resource wrong: %v", payload)
	}
	regBody := `{"client_name":"Claude Code","redirect_uris":["http://localhost:11111/callback"]}`
	registration := makeRequest("POST", "/oauth/register", "shop.example.com", "https", "127.0.0.1:8123", regBody)
	if registration.Code != 201 {
		t.Fatalf("DCR registration=%d body=%s", registration.Code, registration.Body)
	}
	clientID := ""
	var data map[string]any
	if err := json.Unmarshal(registration.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	clientID, _ = data["client_id"].(string)
	if !strings.HasPrefix(clientID, "djmc_") {
		t.Fatalf("DCR client id %s", clientID)
	}
	verifier := strings.Repeat("A", 50)
	d := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(d[:])
	params := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"http://localhost:11111/callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"resource": {"https://shop.example.com/mcp"}, "scope": {"catalog:read"},
	}
	grant := makeRequest("GET", "/oauth/authorize?"+params.Encode(), "shop.example.com", "https", "127.0.0.1:8123", "")
	if grant.Code != 302 || !strings.HasPrefix(grant.Header().Get("Location"), "/dj-secret-admin/ai-authorize?request=") {
		t.Fatalf("authorize redirect %d %s", grant.Code, grant.Header().Get("Location"))
	}
}

func TestOAuthDenialReturnsErrorAndCannotRedeem(t *testing.T) {
	_, _, remote := remoteFixture(t)
	ctx := context.Background()
	if _, err := remote.SetConfig(ctx, true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	client, err := remote.RegisterClient(ctx, "Claude Code", []string{"http://127.0.0.1:3145/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("V", 64)
	h := sha256.Sum256([]byte(verifier))
	pending, err := remote.NewAuthorization(ctx, client.ClientID, "http://127.0.0.1:3145/callback", base64.RawURLEncoding.EncodeToString(h[:]), "safe-state", "catalog:read", "")
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := remote.Deny(ctx, pending.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "safe-state" || u.Query().Get("iss") != "https://shop.example.com" {
		t.Fatalf("invalid deny callback %s", redirect)
	}
	if _, _, err = remote.Pending(ctx, pending.ID); err == nil {
		t.Fatal("denied pending still usable")
	}
	if _, err = remote.Approve(ctx, pending.ID, 9, []string{"catalog:read"}); err == nil {
		t.Fatal("denied authorization can be approved")
	}
	if _, err = remote.Deny(ctx, pending.ID, 9); err == nil {
		t.Fatal("denial should be single-use")
	}
}

func TestOAuthDatabaseRejectsDoubleApprovalAndCodeReuse(t *testing.T) {
	db, _, remote := remoteFixture(t)
	ctx := context.Background()
	if _, err := remote.SetConfig(ctx, true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	client, err := remote.RegisterClient(ctx, "OpenClaw", []string{"http://localhost:6123/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("x", 64)
	d := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(d[:])
	pending, err := remote.NewAuthorization(ctx, client.ClientID, "http://localhost:6123/callback", challenge, "", "catalog:read", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Approve(ctx, pending.ID, 5, []string{"catalog:read"}); err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Approve(ctx, pending.ID, 5, []string{"catalog:read"}); err == nil {
		t.Fatal("second authorization approval accepted")
	}
	var row domain.AuthorizationRequest
	if err := db.First(&row, "id = ?", pending.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.ApprovedAt == nil || row.CodeHash == "" || row.RedeemedAt != nil {
		t.Fatal("pending authorization state incorrect")
	}
}
