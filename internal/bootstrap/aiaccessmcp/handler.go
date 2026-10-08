package aiaccessmcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dujiao-next/internal/app/container"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	reportingdomain "github.com/dujiao-next/internal/modules/reporting/domain"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
)

type serverContextKey struct{}
type Handler struct {
	services  *container.Container
	transport *mcp.StreamableHTTPHandler
}

func New(services *container.Container) *Handler {
	h := &Handler{services: services}
	h.transport = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		v := r.Context().Value(serverContextKey{})
		if server, ok := v.(*mcp.Server); ok {
			return server
		}
		return nil
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 262144,
		PropagateRequestCancellation: true,
		// Strict Host/Origin/HTTPS/proxy checks happen in Serve before SDK.
		// The SDK's localhost rule rejects valid localhost reverse-proxies.
		DisableLocalhostProtection: true,
	})
	return h
}
func (h *Handler) Serve(c *gin.Context) {
	remote, err := h.services.AiRemoteService.Active(c.Request.Context())
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	origin, err := url.Parse(remote.PublicOrigin)
	if err != nil || !strings.EqualFold(c.Request.Host, origin.Host) {
		c.AbortWithStatus(http.StatusMisdirectedRequest)
		return
	}
	if !aiapp.SecureProxyRequest(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	// MCP clients without Origin are allowed. A browser's cross-site Origin is
	// never permitted, even with a valid bearer token (DNS rebinding/CSRF).
	if from := c.GetHeader("Origin"); from != "" && from != remote.PublicOrigin {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Header("Cache-Control", "no-store")
	if c.Request.Method != http.MethodPost {
		c.Header("Allow", "POST")
		c.AbortWithStatus(http.StatusMethodNotAllowed)
		return
	}
	tokenHeader := c.GetHeader("Authorization")
	resource := remote.PublicOrigin + "/mcp"
	var key *aidomain.Key
	if strings.HasPrefix(tokenHeader, "Bearer ") && len(tokenHeader) < 160 {
		key, err = h.services.AiAccessService.AuthenticateMCP(c.Request.Context(), strings.TrimPrefix(tokenHeader, "Bearer "), resource)
	} else {
		err = aiapp.ErrNotAuthorized
	}
	if err != nil {
		if !errors.Is(err, aiapp.ErrNotAuthorized) {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		c.Header("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, remote.PublicOrigin))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	server := h.makeServer(key, strings.TrimPrefix(tokenHeader, "Bearer "), resource)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), serverContextKey{}, server))
	h.transport.ServeHTTP(c.Writer, c.Request)
}

type Search struct {
	Keyword  string `json:"keyword,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}
type NoArgs struct{}
type ReportInput struct {
	Date     string `json:"date,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}
type Draft struct {
	CategoryID  uint   `json:"category_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Price       string `json:"price"`
	Description string `json:"description,omitempty"`
}

func (h *Handler) makeServer(key *aidomain.Key, token, resource string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "dujiao-next", Version: "2.0.0"}, nil)
	check := func(ctx context.Context, scope, tool string) error {
		current, err := h.services.AiAccessService.AuthenticateMCP(ctx, token, resource)
		if err != nil || !aiapp.HasScope(current.Scopes, scope) {
			return errors.New("MCP permission denied or credential expired")
		}
		if err := h.services.AiAccessService.RecordUse(ctx, current, "mcp/"+tool, "authorized"); err != nil {
			return errors.New("MCP audit unavailable")
		}
		return nil
	}
	if aiapp.HasScope(key.Scopes, aiapp.ScopeCatalog) {
		mcp.AddTool(server, &mcp.Tool{Name: "list_products", Description: "Read-only, sanitized product names, slugs, price, status and stock."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in Search) (*mcp.CallToolResult, map[string]any, error) {
				if err := check(ctx, aiapp.ScopeCatalog, "list_products"); err != nil {
					return nil, nil, err
				}
				if len(in.Keyword) > 120 {
					return nil, nil, errors.New("keyword too long")
				}
				if in.Page == 0 {
					in.Page = 1
				}
				if in.PageSize == 0 {
					in.PageSize = 20
				}
				if in.Page < 1 || in.Page > 10000 || in.PageSize < 1 || in.PageSize > 50 {
					return nil, nil, errors.New("invalid pagination")
				}
				rows, _, err := h.services.ProductReadService.ListAdmin("", in.Keyword, "", "", nil, nil, 0, in.Page, in.PageSize)
				if err != nil {
					return nil, nil, errors.New("products unavailable")
				}
				if err = h.services.ProductReadService.ApplyAutoStockCounts(rows); err != nil {
					return nil, nil, errors.New("stock unavailable")
				}
				out := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					out = append(out, map[string]any{
						"id": r.ID, "slug": r.Slug, "title": r.TitleJSON, "price_amount": r.PriceAmount,
						"is_active": r.IsActive, "fulfillment_type": r.FulfillmentType,
						"manual_stock_total": r.ManualStockTotal,
					})
				}
				return nil, map[string]any{"products": out}, nil
			})
		mcp.AddTool(server, &mcp.Tool{Name: "list_categories", Description: "Read-only sanitized product categories."},
			func(ctx context.Context, _ *mcp.CallToolRequest, _ NoArgs) (*mcp.CallToolResult, map[string]any, error) {
				if err := check(ctx, aiapp.ScopeCatalog, "list_categories"); err != nil {
					return nil, nil, err
				}
				rows, err := h.services.CategoryService.List()
				if err != nil {
					return nil, nil, errors.New("categories unavailable")
				}
				out := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					out = append(out, map[string]any{"id": r.ID, "slug": r.Slug, "name": r.NameJSON, "is_active": r.IsActive})
				}
				return nil, map[string]any{"categories": out}, nil
			})
	}
	if aiapp.HasScope(key.Scopes, aiapp.ScopeInventory) {
		mcp.AddTool(server, &mcp.Tool{Name: "list_inventory_alerts", Description: "Read-only low-stock/out-of-stock alerts, no secret inventory values."},
			func(ctx context.Context, _ *mcp.CallToolRequest, _ NoArgs) (*mcp.CallToolResult, map[string]any, error) {
				if err := check(ctx, aiapp.ScopeInventory, "list_inventory_alerts"); err != nil {
					return nil, nil, err
				}
				settings := h.services.DashboardService.LoadDashboardAlertSetting()
				rows, err := h.services.DashboardService.GetInventoryAlertItems(ctx, settings.LowStockThreshold)
				if err != nil {
					return nil, nil, errors.New("inventory unavailable")
				}
				out := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					out = append(out, map[string]any{
						"product_id": r.ProductID, "sku_id": r.SKUID, "product_title": r.ProductTitleJSON,
						"alert_type": r.AlertType, "available_stock": r.AvailableStock,
					})
				}
				return nil, map[string]any{"alerts": out}, nil
			})
	}
	if aiapp.HasScope(key.Scopes, aiapp.ScopeReport) {
		mcp.AddTool(server, &mcp.Tool{Name: "daily_sales_summary", Description: "Read-only daily paid GMV, order counts, estimated profit and top 3 products."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in ReportInput) (*mcp.CallToolResult, map[string]any, error) {
				if err := check(ctx, aiapp.ScopeReport, "daily_sales_summary"); err != nil {
					return nil, nil, err
				}
				if in.Timezone == "" {
					in.Timezone = "Asia/Shanghai"
				}
				if len(in.Timezone) > 80 || len(in.Date) > 30 {
					return nil, nil, errors.New("invalid date or timezone")
				}
				tz, err := time.LoadLocation(in.Timezone)
				if err != nil {
					return nil, nil, errors.New("unknown timezone")
				}
				day := time.Now().In(tz).AddDate(0, 0, -1)
				if in.Date == "today" {
					day = time.Now().In(tz)
				} else if in.Date != "" && in.Date != "yesterday" {
					day, err = time.ParseInLocation("2006-01-02", in.Date, tz)
					if err != nil {
						return nil, nil, errors.New("invalid report date")
					}
				}
				begin := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
				end := begin.AddDate(0, 0, 1).Add(-time.Second)
				input := reportingdomain.Query{Range: "custom", From: &begin, To: &end, Timezone: in.Timezone}
				overview, err := h.services.DashboardService.GetOverview(ctx, input)
				if err != nil {
					return nil, nil, errors.New("report unavailable")
				}
				rankings, err := h.services.DashboardService.GetRankings(ctx, input)
				if err != nil {
					return nil, nil, errors.New("rankings unavailable")
				}
				top := make([]map[string]any, 0, 3)
				for _, r := range rankings.TopProducts {
					top = append(top, map[string]any{"title": r.Title, "quantity": r.Quantity, "paid_amount": r.PaidAmount})
					if len(top) == 3 {
						break
					}
				}
				k := overview.KPI
				return nil, map[string]any{
					"date": begin.Format("2006-01-02"), "timezone": in.Timezone, "currency": overview.Currency,
					"kpi": map[string]any{
						"gmv_paid": k.GMVPaid, "orders_total": k.OrdersTotal, "paid_orders": k.PaidOrders,
						"completed_orders": k.CompletedOrders, "pending_payment_orders": k.PendingPaymentOrders,
						"processing_orders": k.ProcessingOrders, "total_cost": k.TotalCost,
						"total_profit": k.TotalProfit, "profit_margin": k.ProfitMargin,
						"payment_fee": k.PaymentFee, "payments_failed": k.PaymentsFailed,
						"out_of_stock_products": k.OutOfStockProducts, "low_stock_products": k.LowStockProducts,
					},
					"top_products": top, "accounting_note": "Dashboard estimates; not an audited financial statement",
				}, nil
			})
	}
	// Offline preview is always safe regardless of read scopes, as no merchant
	// DB/network operation is performed. It is NOT a product-create capability.
	mcp.AddTool(server, &mcp.Tool{Name: "preview_product_draft", Description: "Offline-only draft preview; never writes product, price, stock, or publication state."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in Draft) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, key.Scopes[:strings.Index(key.Scopes+",", ",")], "preview_product_draft"); err != nil {
				return nil, nil, err
			}
			slugPattern := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?$`)
			if in.CategoryID == 0 || !slugPattern.MatchString(in.Slug) || len(in.Title) == 0 || len(in.Title) > 120 || len(in.Description) > 1000 {
				return nil, nil, errors.New("invalid draft input")
			}
			value, err := decimal.NewFromString(in.Price)
			if err != nil || value.LessThan(decimal.NewFromInt(0).Add(decimal.NewFromFloat(0.01))) || value.GreaterThan(decimal.NewFromInt(999999).Add(decimal.NewFromFloat(0.99))) || value.Exponent() < -2 {
				return nil, nil, errors.New("invalid draft price")
			}
			return nil, map[string]any{
				"preview_only": true, "written_to_store": false, "can_publish": false,
				"payload": map[string]any{
					"category_id": in.CategoryID, "slug": in.Slug, "title": map[string]string{"zh-CN": in.Title},
					"description": map[string]string{"zh-CN": in.Description}, "price_amount": value.StringFixed(2),
					"fulfillment_type": "manual", "manual_stock_total": 0, "is_active": false,
				},
			}, nil
		})
	return server
}
