package aiaccessroutes

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dujiao-next/internal/app/container"
	"github.com/dujiao-next/internal/i18n"
	aiaccessapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aiaccesshttp "github.com/dujiao-next/internal/modules/aiaccess/transport/http"
	reportinghttp "github.com/dujiao-next/internal/modules/reporting/transport/http"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/gin-gonic/gin"
)

func aiBad(c *gin.Context) {
	response.ErrorWithHTTPStatus(c, http.StatusBadRequest, response.CodeBadRequest, i18n.T(i18n.ResolveLocale(c), "error.bad_request"))
}
func aiUnavailable(c *gin.Context) {
	response.ErrorWithHTTPStatus(c, http.StatusServiceUnavailable, response.CodeInternal, i18n.T(i18n.ResolveLocale(c), "error.internal_error"))
}

// Register is intentionally disjoint from /admin: a valid AI machine
// key cannot be used as an admin JWT or hit any admin write endpoint.
func Register(api *gin.RouterGroup, services *container.Container) {
	if services == nil || services.AiAccessService == nil {
		return
	}
	group := api.Group("/ai")
	guard := func(scope, route string) gin.HandlerFunc {
		return aiaccesshttp.MachineMiddleware(services.AiAccessService, scope, route)
	}

	group.GET("/products", guard(aiaccessapp.ScopeCatalog, "/ai/products"), func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		search := c.Query("search")
		if page < 1 || page > 10000 || size < 1 || size > 50 || len(search) > 120 {
			aiBad(c)
			return
		}
		rows, _, err := services.ProductReadService.ListAdmin("", search, "", "", nil, nil, 0, page, size)
		if err != nil {
			aiUnavailable(c)
			return
		}
		if err = services.ProductReadService.ApplyAutoStockCounts(rows); err != nil {
			aiUnavailable(c)
			return
		}
		items := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			items = append(items, gin.H{
				"id": r.ID, "slug": r.Slug, "title": r.TitleJSON,
				"price_amount": r.PriceAmount, "is_active": r.IsActive,
				"fulfillment_type": r.FulfillmentType, "manual_stock_total": r.ManualStockTotal,
			})
		}
		response.Success(c, items)
	})

	group.GET("/categories", guard(aiaccessapp.ScopeCatalog, "/ai/categories"), func(c *gin.Context) {
		rows, err := services.CategoryService.List()
		if err != nil {
			aiUnavailable(c)
			return
		}
		items := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			items = append(items, gin.H{"id": r.ID, "slug": r.Slug, "name": r.NameJSON, "is_active": r.IsActive})
		}
		response.Success(c, items)
	})

	group.GET("/dashboard/inventory-alerts", guard(aiaccessapp.ScopeInventory, "/ai/dashboard/inventory-alerts"), func(c *gin.Context) {
		settings := services.DashboardService.LoadDashboardAlertSetting()
		rows, err := services.DashboardService.GetInventoryAlertItems(c.Request.Context(), settings.LowStockThreshold)
		if err != nil {
			aiUnavailable(c)
			return
		}
		items := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			items = append(items, gin.H{
				"product_id": r.ProductID, "sku_id": r.SKUID,
				"product_title": r.ProductTitleJSON,
				"alert_type":    r.AlertType, "available_stock": r.AvailableStock,
			})
		}
		response.Success(c, items)
	})

	parseReport := func(c *gin.Context) (bool, string) {
		rangeName := strings.TrimSpace(c.DefaultQuery("range", "7d"))
		switch rangeName {
		case "today", "yesterday", "7d", "30d", "custom":
		default:
			aiBad(c)
			return false, ""
		}
		return true, rangeName
	}
	group.GET("/dashboard/overview", guard(aiaccessapp.ScopeReport, "/ai/dashboard/overview"), func(c *gin.Context) {
		if ok, _ := parseReport(c); !ok {
			return
		}
		query, err := reportinghttp.ParseQuery(c)
		if err != nil {
			aiBad(c)
			return
		}
		if query.Range == "custom" && (query.From == nil || query.To == nil || query.To.Sub(*query.From) > 32*24*time.Hour) {
			aiBad(c)
			return
		}
		query.ForceRefresh = false
		overview, err := services.DashboardService.GetOverview(c.Request.Context(), query)
		if err != nil {
			aiBad(c)
			return
		}
		kpi := overview.KPI
		response.Success(c, gin.H{"currency": overview.Currency, "kpi": gin.H{
			"orders_total": kpi.OrdersTotal, "paid_orders": kpi.PaidOrders,
			"completed_orders":       kpi.CompletedOrders,
			"pending_payment_orders": kpi.PendingPaymentOrders,
			"processing_orders":      kpi.ProcessingOrders,
			"gmv_paid":               kpi.GMVPaid, "total_cost": kpi.TotalCost,
			"total_profit": kpi.TotalProfit, "payment_fee": kpi.PaymentFee,
			"profit_margin": kpi.ProfitMargin, "payments_failed": kpi.PaymentsFailed,
			"payment_success_rate":  kpi.PaymentSuccessRate,
			"low_stock_products":    kpi.LowStockProducts,
			"out_of_stock_products": kpi.OutOfStockProducts, "new_users": kpi.NewUsers,
		}})
	})
	group.GET("/dashboard/rankings", guard(aiaccessapp.ScopeReport, "/ai/dashboard/rankings"), func(c *gin.Context) {
		if ok, _ := parseReport(c); !ok {
			return
		}
		query, err := reportinghttp.ParseQuery(c)
		if err != nil {
			aiBad(c)
			return
		}
		if query.Range == "custom" && (query.From == nil || query.To == nil || query.To.Sub(*query.From) > 32*24*time.Hour) {
			aiBad(c)
			return
		}
		query.ForceRefresh = false
		rankings, err := services.DashboardService.GetRankings(c.Request.Context(), query)
		if err != nil {
			aiBad(c)
			return
		}
		top := make([]gin.H, 0, 3)
		for _, row := range rankings.TopProducts {
			top = append(top, gin.H{"title": row.Title, "quantity": row.Quantity, "paid_amount": row.PaidAmount})
			if len(top) == 3 {
				break
			}
		}
		response.Success(c, gin.H{"top_products": top})
	})
}
