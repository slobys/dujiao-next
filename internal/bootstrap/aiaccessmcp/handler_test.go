package aiaccessmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/app/container"
	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

type rewritingTransport struct {
	Base  http.RoundTripper
	Token string
}

func (t rewritingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := req.Clone(req.Context())
	next.Host = "shop.example.com"
	next.Header.Set("X-Forwarded-Proto", "https")
	if t.Token != "" {
		next.Header.Set("Authorization", "Bearer "+t.Token)
	}
	return t.Base.RoundTrip(next)
}
func fixture(t *testing.T) (*container.Container, *app.Service, *app.RemoteService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:mcp_http_test_"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&domain.Key{}, &domain.Audit{}, &domain.RemoteConfig{}, &domain.OAuthClient{}, &domain.AuthorizationRequest{}, &domain.OAuthRefresh{}, &domain.ActionRequest{}, &domain.OrderReview{}, &domain.OrderCancellation{}); err != nil {
		t.Fatal(err)
	}
	store := gormstore.New(db)
	keys := app.New(store)
	remote := app.NewRemote(store, keys)
	return &container.Container{AiAccessService: keys, AiRemoteService: remote, AiActionService: app.NewActions(store), AiOrderReviewService: app.NewOrderReviews(store), AiOrderCancellationService: app.NewOrderCancellations(store)}, keys, remote
}
func issueOAuthToken(t *testing.T, remote *app.RemoteService, scopes string) string {
	t.Helper()
	ctx := context.Background()
	client, err := remote.RegisterClient(ctx, "Codex", []string{"http://localhost:1455/auth/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 64)
	sum := sha256.Sum256([]byte(verifier))
	pending, err := remote.NewAuthorization(ctx, client.ClientID, "http://localhost:1455/auth/callback",
		base64.RawURLEncoding.EncodeToString(sum[:]), "mcp-client-state", scopes, "https://shop.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := remote.Approve(ctx, pending.ID, 5, strings.Fields(scopes))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	token, err := remote.Exchange(ctx, client.ClientID, u.Query().Get("code"),
		"http://localhost:1455/auth/callback", verifier, "https://shop.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	return token.AccessToken
}
func TestOfficialGoSDKConnectAndPreview(t *testing.T) {
	services, keys, remote := fixture(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Any("/mcp", New(services).Serve)
	httpServer := httptest.NewServer(r)
	defer httpServer.Close()
	httpClient := &http.Client{Transport: rewritingTransport{Base: http.DefaultTransport, Token: ""}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	bad, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/mcp", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	bad.Host = "shop.example.com"
	bad.Header.Set("X-Forwarded-Proto", "https")
	resp, err := httpClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("default remote not 404, got %d", resp.StatusCode)
	}
	if _, err = remote.SetConfig(ctx, true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	resp, err = httpClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated remote not 401: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "oauth-protected-resource/mcp") {
		t.Fatal("missing OAuth resource metadata")
	}
	access := issueOAuthToken(t, remote, "catalog:read")
	for _, scenario := range []struct {
		host, origin, proto string
		status              int
	}{
		{"attacker.example", "", "https", 421},
		{"shop.example.com", "https://attacker.example", "https", 403},
		{"shop.example.com", "", "http", 403},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/mcp", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = scenario.host
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("X-Forwarded-Proto", scenario.proto)
		if scenario.origin != "" {
			req.Header.Set("Origin", scenario.origin)
		}
		got, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got.Body.Close()
		if got.StatusCode != scenario.status {
			t.Fatalf("unsafe Host/Origin/proto accepted: %#v got %d", scenario, got.StatusCode)
		}
	}
	officialClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: rewritingTransport{Base: http.DefaultTransport, Token: access}},
		DisableStandaloneSSE: true, MaxRetries: -1,
	}
	session, err := officialClient.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("official MCP SDK handshake: %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if !names["list_products"] || !names["list_categories"] || !names["preview_product_draft"] {
		t.Fatalf("missing catalog tools: %v", names)
	}
	if names["daily_sales_summary"] || names["list_inventory_alerts"] {
		t.Fatalf("exposed ungranted tools: %v", names)
	}
	if names["create_product_draft"] || names["request_product_status_change"] {
		t.Fatalf("write tools exposed without explicit consent: %v", names)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "preview_product_draft",
		Arguments: map[string]any{"category_id": 1, "slug": "demo", "title": "测试", "price": "29.90"},
	})
	if err != nil || result.IsError {
		t.Fatalf("official client tool call: %+v %v", result, err)
	}
	body, ok := result.StructuredContent.(map[string]any)
	if !ok || body["preview_only"] != true {
		t.Fatalf("preview result=%+v", result)
	}
	records, err := keys.Audits(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	for _, a := range records {
		if a.Action == "access" && a.Route == "mcp/preview_product_draft" {
			observed = true
		}
	}
	if !observed {
		t.Fatal("missing audit for actual MCP tool call")
	}
	if _, err = remote.SetConfig(ctx, false, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	_, err = session.ListTools(ctx, nil)
	if err == nil {
		t.Fatal("MCP call remained active after remote access disabled")
	}
}
