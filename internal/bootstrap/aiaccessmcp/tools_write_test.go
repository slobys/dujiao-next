package aiaccessmcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	catalogproductbootstrap "github.com/dujiao-next/internal/bootstrap/catalogproduct"
	categorydomain "github.com/dujiao-next/internal/modules/catalog/category/domain"
	categorygormstore "github.com/dujiao-next/internal/modules/catalog/category/infrastructure/gormstore"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

func TestMCPDraftCreationAndPublishRequestNeedSeparateConsent(t *testing.T) {
	services, _, remote := fixture(t)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:ai_mcp_draft_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&categorydomain.Category{}, &productdomain.Product{}, &productdomain.ProductSKU{}); err != nil {
		t.Fatal(err)
	}
	cat := categorydomain.Category{
		ID: 1, Slug: "ai-test", NameJSON: jsonmap.JSON{"zh-CN": "AI 草稿"}, IsActive: true,
	}
	if err = db.Create(&cat).Error; err != nil {
		t.Fatal(err)
	}
	productRepo := productgormstore.NewProductStore(db)
	productSvc := catalogproductbootstrap.New(catalogproductbootstrap.Dependencies{
		Products: productRepo, SKUs: productgormstore.NewSKUStore(db),
		Categories: categorygormstore.NewCategoryStore(db),
	})
	services.ProductReadService = productSvc.Read
	services.ProductWriteService = productSvc.Write
	if _, err = remote.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	token := issueOAuthToken(t, remote, "catalog:draft:write catalog:publish:request")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/mcp", New(services).Serve)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-write-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: rewritingTransport{Base: http.DefaultTransport, Token: token}},
		DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listing, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, tool := range listing.Tools {
		available[tool.Name] = true
	}
	if !available["create_product_draft"] || !available["request_product_status_change"] || !available["get_product_action_status"] {
		t.Fatalf("authorized write tool missing: %v", available)
	}
	if available["list_products"] {
		t.Fatal("ungranted catalog read tool exposed")
	}
	output, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_product_draft", Arguments: map[string]any{
			"category_id": 1, "slug": "ai-test-draft", "title": "AI 新建测试商品",
			"price": "29.90", "description": "由 AI 生成，需人工审核后上架",
		},
	})
	if err != nil || output.IsError {
		t.Fatalf("real draft create failed: %+v %v", output, err)
	}
	info, ok := output.StructuredContent.(map[string]any)
	if !ok || info["created"] != true || info["published"] != false {
		t.Fatalf("invalid response %+v", output)
	}
	var saved productdomain.Product
	if err = db.Where("slug = ?", "ai-test-draft").First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.IsActive || saved.ManualStockTotal != 0 || saved.FulfillmentType != "manual" || saved.PriceAmount.String() != "29.90" {
		t.Fatalf("AI was allowed to publish/stock/change fulfillment: %+v", saved)
	}
	var count int64
	if err = db.Model(&productdomain.ProductSKU{}).Where("product_id = ?", saved.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("created draft missing default SKU: count %d err %v", count, err)
	}
	repeat, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_product_draft", Arguments: map[string]any{
			"category_id": 1, "slug": "ai-test-draft", "title": "Duplicate", "price": "29.90",
		},
	})
	if err == nil && !repeat.IsError {
		t.Fatal("duplicate draft silently created")
	}
	malicious, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_product_draft", Arguments: map[string]any{
			"category_id": 1, "slug": "evil", "title": "<script>steal()</script>", "price": "29.90",
		},
	})
	if err == nil && !malicious.IsError {
		t.Fatal("HTML injection accepted")
	}
	req, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_product_status_change",
		Arguments: map[string]any{"product_id": saved.ID, "publish": true},
	})
	if err != nil || req.IsError {
		t.Fatalf("request create failed: %+v %v", req, err)
	}
	payload, ok := req.StructuredContent.(map[string]any)
	if !ok || payload["requires_human_approval"] != true || payload["executed"] != false {
		t.Fatalf("dangerous request executed: %+v", req)
	}
	current, err := productRepo.GetByID(fmt.Sprint(saved.ID))
	if err != nil || current.IsActive {
		t.Fatal("request alone published product")
	}
	status, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_product_action_status", Arguments: map[string]any{"request_id": payload["request_id"]},
	})
	if err != nil || status.IsError {
		t.Fatalf("cannot check own request: %+v %v", status, err)
	}
	state, _ := status.StructuredContent.(map[string]any)
	if state["status"] != "pending" {
		t.Fatalf("request not pending: %+v", state)
	}
}
