package aiaccessmcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	catalogproductbootstrap "github.com/dujiao-next/internal/bootstrap/catalogproduct"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	categorydomain "github.com/dujiao-next/internal/modules/catalog/category/domain"
	categorygormstore "github.com/dujiao-next/internal/modules/catalog/category/infrastructure/gormstore"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"
	ordercontract "github.com/dujiao-next/internal/modules/order/contract"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
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

// Mocked order store implements ONLY the two read methods exercised by MCP;
// the full domain Store remains embedded but none of its write methods can run.
type aiOrderStoreStub struct {
	ordercontract.Store
	order orderdomain.Order
}

func (s *aiOrderStoreStub) GetByID(id uint) (*orderdomain.Order, error) {
	if id != s.order.ID {
		return nil, nil
	}
	item := s.order
	return &item, nil
}
func (s *aiOrderStoreStub) ListAdmin(filter ordercontract.ListFilter) ([]orderdomain.Order, int64, error) {
	if filter.Status != "" && filter.Status != s.order.Status {
		return []orderdomain.Order{}, 0, nil
	}
	return []orderdomain.Order{s.order}, 1, nil
}
func TestMCPOrderSummaryRedactionAndTriage(t *testing.T) {
	services, _, remote := fixture(t)
	now := time.Now().UTC()
	store := &aiOrderStoreStub{order: orderdomain.Order{
		ID: 1001, OrderNo: "DJ-TEST-PRIVATE-0001", Status: "paid",
		Currency: "CNY", TotalAmount: money.FromDecimal(decimal.RequireFromString("98.50")),
		GuestEmail: "customer.secret@example.org", ClientIP: "203.0.113.77",
		GuestPassword: "DO-NOT-LEAK-PASSWORD",
		Items:         []orderdomain.OrderItem{{OrderID: 1001, TitleJSON: jsonmap.JSON{"zh-CN": "DELIVERY_SECRET_IN_ITEM"}}},
		CreatedAt:     now.Add(-time.Hour), UpdatedAt: now, PaidAt: &now,
	}}
	services.OrderStore = store
	if _, err := remote.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	access := issueOAuthToken(t, remote, "orders:read orders:review:request")
	router := gin.New()
	gin.SetMode(gin.TestMode)
	router.Any("/mcp", New(services).Serve)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "order-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: rewritingTransport{Base: http.DefaultTransport, Token: access}},
		DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"list_order_summaries", "get_order_summary", "request_order_after_sales_review", "get_order_after_sales_review_status"} {
		if !names[name] {
			t.Fatalf("missing consented order tool %s", name)
		}
	}
	if names["create_product_draft"] || names["request_product_status_change"] || names["list_products"] {
		t.Fatalf("order scopes allowed product writes/reads: %v", names)
	}
	summaries, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_order_summaries", Arguments: map[string]any{"status": "paid", "page": 1, "page_size": 5}})
	if err != nil || summaries.IsError {
		t.Fatalf("list failed: %+v %v", summaries, err)
	}
	single, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_order_summary", Arguments: map[string]any{"order_id": 1001}})
	if err != nil || single.IsError {
		t.Fatalf("single failed: %+v %v", single, err)
	}
	for _, data := range []any{summaries.StructuredContent, single.StructuredContent} {
		value := fmt.Sprint(data)
		for _, secret := range []string{"customer.secret@example.org", "203.0.113.77", "DO-NOT-LEAK-PASSWORD", "DELIVERY_SECRET_IN_ITEM"} {
			if strings.Contains(value, secret) {
				t.Fatalf("sensitive order data escaped to AI: %s", secret)
			}
		}
		if !strings.Contains(value, "98.50") || !strings.Contains(value, "paid") {
			t.Fatalf("missing safe facts: %s", value)
		}
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "request_order_after_sales_review", Arguments: map[string]any{"order_id": 1001, "reason": "refund_execute_now"},
	})
	if err == nil && !invalid.IsError {
		t.Fatal("AI requested direct refund action")
	}
	triage, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "request_order_after_sales_review", Arguments: map[string]any{"order_id": 1001, "reason": "refund_review"},
	})
	if err != nil || triage.IsError {
		t.Fatalf("triage submit failed: %+v %v", triage, err)
	}
	data, ok := triage.StructuredContent.(map[string]any)
	if !ok || data["status"] != "pending" || data["refund_processed"] != false || data["order_modified"] != false {
		t.Fatalf("triage unexpectedly performed financial action: %+v", triage)
	}
	reqID, ok := data["request_id"].(string)
	if !ok {
		t.Fatal("no request id")
	}
	observed, err := services.AiOrderReviewService.GetForAdmin(ctx, reqID)
	if err != nil || observed.ExpectedTotal != "98.50" || observed.ExpectedStatus != "paid" {
		t.Fatalf("unpersisted request %+v %v", observed, err)
	}
	if store.order.Status != "paid" {
		t.Fatal("triage changed real merchant order")
	}
	if state, err := services.AiOrderReviewService.Process(ctx, reqID, 7, "accept"); err != nil || state != aidomain.OrderReviewAccepted {
		t.Fatalf("human acceptance %s %v", state, err)
	}
	report, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_order_after_sales_review_status", Arguments: map[string]any{"request_id": reqID},
	})
	if err != nil || report.IsError {
		t.Fatalf("status query failed: %+v %v", report, err)
	}
	status, _ := report.StructuredContent.(map[string]any)
	if status["status"] != "accepted" {
		t.Fatalf("not accepted: %+v", status)
	}
	if store.order.Status != "paid" {
		t.Fatal("human triage acceptance changed paid order")
	}
}

func TestMCPStrictUnpaidCancellationRequiresScopeAndOnlySubmitsRequest(t *testing.T) {
	services, _, remote := fixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	store := &aiOrderStoreStub{order: orderdomain.Order{
		ID: 2002, OrderNo: "UNPAID-PRIVATE-2002",
		Currency: "CNY", Status: "pending_payment",
		TotalAmount:      money.FromDecimal(decimal.RequireFromString("88.00")),
		WalletPaidAmount: money.FromDecimal(decimal.Zero),
		GuestEmail:       "PRIVATE@example.invalid",
		CreatedAt:        now.Add(-time.Minute), UpdatedAt: now,
	}}
	services.OrderStore = store
	if _, err := remote.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	access := issueOAuthToken(t, remote, "orders:cancel:request")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/mcp", New(services).Serve)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "cancel-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: rewritingTransport{Base: http.DefaultTransport, Token: access}},
		DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	for _, tool := range tools.Tools {
		visible[tool.Name] = true
	}
	if !visible["request_strict_unpaid_order_cancellation"] ||
		!visible["get_strict_unpaid_order_cancellation_status"] {
		t.Fatalf("missing cancellation tools: %+v", visible)
	}
	if visible["list_order_summaries"] || visible["request_order_after_sales_review"] || visible["create_product_draft"] {
		t.Fatalf("cancellation permission escalated: %+v", visible)
	}
	pending, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_strict_unpaid_order_cancellation",
		Arguments: map[string]any{"order_id": 2002},
	})
	if err != nil || pending.IsError {
		t.Fatalf("submission failed: %+v %v", pending, err)
	}
	result, ok := pending.StructuredContent.(map[string]any)
	if !ok || result["status"] != "pending" || result["executed"] != false ||
		result["requires_human_approval"] != true {
		t.Fatalf("submission mutated order: %+v", pending)
	}
	if strings.Contains(fmt.Sprint(result), "PRIVATE@example.invalid") {
		t.Fatal("MCP leaked order email")
	}
	if store.order.Status != "pending_payment" {
		t.Fatal("MCP canceled order without approval")
	}
	requestID, _ := result["request_id"].(string)
	if requestID == "" {
		t.Fatal("missing request id")
	}
	persisted, err := services.AiOrderCancellationService.GetForAdmin(ctx, requestID)
	if err != nil || persisted == nil || persisted.Status != "pending" || persisted.Currency != "CNY" {
		t.Fatalf("wrong persisted request %+v %v", persisted, err)
	}
	lookup, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_strict_unpaid_order_cancellation_status",
		Arguments: map[string]any{"request_id": requestID},
	})
	if err != nil || lookup.IsError {
		t.Fatalf("cannot read own request: %+v %v", lookup, err)
	}
	store.order.Status = "paid"
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_strict_unpaid_order_cancellation",
		Arguments: map[string]any{"order_id": 2002},
	})
	if err == nil && !invalid.IsError {
		t.Fatal("accepted cancellation request for paid order")
	}
}

func TestMCPWalletRefundScopeOnlyProposesFundsNotCredits(t *testing.T) {
	services, _, remote := fixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	store := &aiOrderStoreStub{order: orderdomain.Order{
		ID: 4300, OrderNo: "SENSITIVE-REFUND-ORDER", Status: "paid", Currency: "CNY",
		UserID: 10, PaidAt: &now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		TotalAmount:      money.FromDecimal(decimal.RequireFromString("80.00")),
		WalletPaidAmount: money.FromDecimal(decimal.RequireFromString("80.00")),
		OnlinePaidAmount: money.FromDecimal(decimal.Zero), RefundedAmount: money.FromDecimal(decimal.Zero),
		GuestEmail: "CUSTOMER-WALLET-SECRET@example.test", ClientIP: "203.0.113.17",
	}}
	services.OrderStore = store
	if _, err := remote.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	token := issueOAuthToken(t, remote, "orders:wallet-refund:request")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/mcp", New(services).Serve)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "wallet-ai", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: rewritingTransport{Base: http.DefaultTransport, Token: token}},
		DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	available, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range available.Tools {
		found[tool.Name] = true
	}
	for _, tool := range []string{"preview_strict_wallet_refund", "request_strict_wallet_refund", "get_wallet_refund_request_status"} {
		if !found[tool] {
			t.Fatalf("wallet request tool missing %s: %+v", tool, found)
		}
	}
	if found["list_order_summaries"] || found["create_product_draft"] || found["request_strict_unpaid_order_cancellation"] {
		t.Fatalf("wallet refund consent escalated permissions %+v", found)
	}
	preview, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "preview_strict_wallet_refund",
		Arguments: map[string]any{"order_id": 4300, "amount": "20.00"},
	})
	if err != nil || preview.IsError {
		t.Fatalf("preview failed %+v %v", preview, err)
	}
	pmap, ok := preview.StructuredContent.(map[string]any)
	if !ok || pmap["eligible_for_request"] != true {
		t.Fatalf("expected preflight eligibility %+v", preview)
	}
	if strings.Contains(fmt.Sprint(pmap), "CUSTOMER-WALLET-SECRET@example.test") {
		t.Fatal("refund preflight leaked customer email")
	}
	rejected, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_strict_wallet_refund",
		Arguments: map[string]any{"order_id": 4300, "amount": "600.00", "reason": "customer_request"},
	})
	if err == nil && !rejected.IsError {
		t.Fatal("oversized wallet refund accepted")
	}
	request, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_strict_wallet_refund",
		Arguments: map[string]any{"order_id": 4300, "amount": "20.00", "reason": "customer_request"},
	})
	if err != nil || request.IsError {
		t.Fatalf("wallet request failed %+v %v", request, err)
	}
	body, ok := request.StructuredContent.(map[string]any)
	if !ok || body["executed"] != false || body["requires_human_approval"] != true || body["wallet_credited"] != false {
		t.Fatalf("AI executed wallet credit instead of proposing it %+v", request)
	}
	if store.order.Status != "paid" || store.order.RefundedAmount.Decimal.Sign() != 0 {
		t.Fatal("AI tool modified financial state")
	}
	id, _ := body["request_id"].(string)
	if id == "" {
		t.Fatal("missing durable approval ID")
	}
	pending, err := services.AiWalletRefundService.GetForAdmin(ctx, id)
	if err != nil || pending == nil || pending.Amount != "20.00" || pending.ExpectedTotal != "80.00" || pending.Status != "pending" {
		t.Fatalf("not persisted %+v %v", pending, err)
	}
	status, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_wallet_refund_request_status", Arguments: map[string]any{"request_id": id},
	})
	if err != nil || status.IsError {
		t.Fatalf("cannot read request status %+v %v", status, err)
	}
	statusMap, _ := status.StructuredContent.(map[string]any)
	if statusMap["status"] != "pending" {
		t.Fatalf("status mismatch %+v", statusMap)
	}
	store.order.OnlinePaidAmount = money.FromDecimal(decimal.NewFromInt(1))
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "request_strict_wallet_refund",
		Arguments: map[string]any{"order_id": 4300, "amount": "20.00", "reason": "customer_request"},
	})
	if err == nil && !invalid.IsError {
		t.Fatal("AI could submit wallet refund for online-paid order")
	}
}
