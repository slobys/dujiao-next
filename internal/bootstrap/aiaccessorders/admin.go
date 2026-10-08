package aiaccessorders

import (
	"errors"
	"net/http"
	"time"

	"github.com/dujiao-next/internal/app/container"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/gin-gonic/gin"
)

type Handler struct{ services *container.Container }

func New(services *container.Container) *Handler { return &Handler{services: services} }
func RegisterAdminRoutes(routes gin.IRoutes, services *container.Container) {
	h := New(services)
	routes.GET("/ai-access/order-reviews", h.List)
	routes.POST("/ai-access/order-reviews/:id/accept", h.Accept)
	routes.POST("/ai-access/order-reviews/:id/reject", h.Reject)
	routes.POST("/ai-access/order-reviews/:id/resolve", h.Resolve)
	RegisterCancellationRoutes(routes, services)
	RegisterWalletRefundRoutes(routes,services)
}
func adminID(c *gin.Context) uint {
	value, _ := c.Get("admin_id")
	id, _ := value.(uint)
	return id
}
func sendError(c *gin.Context, status int) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"status_code": status, "msg": "order review unavailable or already processed", "data": nil})
}
func (h *Handler) List(c *gin.Context) {
	records, err := h.services.AiOrderReviewService.List(c.Request.Context(), 100)
	if err != nil {
		sendError(c, http.StatusServiceUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, records)
}
func (h *Handler) Accept(c *gin.Context) {
	if adminID(c) == 0 {
		sendError(c, http.StatusForbidden)
		return
	}
	ctx := c.Request.Context()
	if _, err := h.services.AiRemoteService.Active(ctx); err != nil {
		sendError(c, http.StatusConflict)
		return
	}
	id := c.Param("id")
	reviews := h.services.AiOrderReviewService
	item, err := reviews.GetForAdmin(ctx, id)
	if err != nil || item == nil || item.Status != aidomain.OrderReviewPending || !time.Now().Before(item.ExpiresAt) {
		sendError(c, http.StatusConflict)
		return
	}
	current, err := h.services.OrderStore.GetByID(item.OrderID)
	if err != nil {
		sendError(c, http.StatusServiceUnavailable)
		return
	}
	if current == nil || current.OrderNo != item.OrderNo || current.Status != item.ExpectedStatus ||
		current.Currency != item.Currency || current.TotalAmount.String() != item.ExpectedTotal ||
		!current.UpdatedAt.Equal(item.ExpectedUpdatedAt) {
		status, e := reviews.Process(ctx, id, adminID(c), "conflict")
		if e != nil || status != aidomain.OrderReviewConflict {
			sendError(c, http.StatusServiceUnavailable)
			return
		}
		sendError(c, http.StatusConflict)
		return
	}
	state, err := reviews.Process(ctx, id, adminID(c), "accept")
	if err != nil || state != aidomain.OrderReviewAccepted {
		sendError(c, http.StatusConflict)
		return
	}
	// IMPORTANT: No order mutation, refund, credit, fulfillment or outbound API.
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"status": state, "order_id": item.OrderID,
		"next_step": "Human must handle after-sales in the ordinary order admin panel; accepting this ticket does not refund, cancel or deliver the order"})
}
func (h *Handler) Reject(c *gin.Context)  { h.change(c, "reject") }
func (h *Handler) Resolve(c *gin.Context) { h.change(c, "resolve") }
func (h *Handler) change(c *gin.Context, transition string) {
	if adminID(c) == 0 {
		sendError(c, http.StatusForbidden)
		return
	}
	id := c.Param("id")
	state, err := h.services.AiOrderReviewService.Process(c.Request.Context(), id, adminID(c), transition)
	if err != nil {
		if errors.Is(err, aiapp.ErrOrderReviewUnavailable) {
			sendError(c, http.StatusConflict)
		} else {
			sendError(c, http.StatusServiceUnavailable)
		}
		return
	}
	if state == "" {
		sendError(c, http.StatusConflict)
		return
	}
	item, _ := h.services.AiOrderReviewService.GetForAdmin(c.Request.Context(), id)
	orderID := uint(0)
	if item != nil {
		orderID = item.OrderID
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"status": state, "order_id": orderID})
}
