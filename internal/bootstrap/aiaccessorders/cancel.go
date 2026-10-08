package aiaccessorders

import (
	"errors"
	"net/http"

	"github.com/dujiao-next/internal/app/container"
	"github.com/dujiao-next/internal/i18n"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/gin-gonic/gin"
)

type narrowOrderCanceller interface {
	CancelStrictlyUnpaidForAI(orderapp.AICancelSnapshot) (*orderdomain.Order, error)
}
type CancellationHandler struct {
	services  *container.Container
	canceller narrowOrderCanceller
}

func NewCancellationHandler(c *container.Container) *CancellationHandler {
	return &CancellationHandler{services: c, canceller: c.OrderService}
}
func RegisterCancellationRoutes(group gin.IRoutes, c *container.Container) {
	h := NewCancellationHandler(c)
	group.GET("/ai-access/order-cancellations", h.List)
	group.POST("/ai-access/order-cancellations/:id/approve", h.Approve)
	group.POST("/ai-access/order-cancellations/:id/reject", h.Reject)
}
func cancelError(c *gin.Context, status int, key string) {
	code := response.CodeBadRequest
	if status >= 500 {
		code = response.CodeInternal
	}
	if status == http.StatusForbidden {
		code = response.CodeUnauthorized
	}
	response.ErrorWithHTTPStatus(c, status, code, i18n.T(i18n.ResolveLocale(c), key))
}
func (h *CancellationHandler) List(c *gin.Context) {
	records, err := h.services.AiOrderCancellationService.List(c.Request.Context(), 100)
	if err != nil {
		cancelError(c, 503, "error.order_fetch_failed")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, records)
}
func (h *CancellationHandler) Reject(c *gin.Context) {
	id := c.Param("id")
	if adminID(c) == 0 {
		cancelError(c, 403, "error.unauthorized")
		return
	}
	if err := h.services.AiOrderCancellationService.Reject(c.Request.Context(), id, adminID(c)); err != nil {
		if errors.Is(err, aiapp.ErrOrderCancellationUnavailable) {
			cancelError(c, 409, "error.order_cancel_not_allowed")
			return
		}
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	response.Success(c, gin.H{"status": aidomain.ActionRejected})
}
func (h *CancellationHandler) Approve(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if adminID(c) == 0 {
		cancelError(c, 403, "error.unauthorized")
		return
	}
	if _, err := h.services.AiRemoteService.Active(ctx); err != nil {
		cancelError(c, 409, "error.order_cancel_not_allowed")
		return
	}
	// Claim and log approval FIRST, then enter merchant order cancellation txn.
	// Claimed requests are never retried automatically, even if process dies.
	requested, err := h.services.AiOrderCancellationService.Claim(ctx, id, adminID(c))
	if err != nil {
		if errors.Is(err, aiapp.ErrOrderCancellationUnavailable) {
			cancelError(c, 409, "error.order_cancel_not_allowed")
			return
		}
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	if requested == nil || h.canceller == nil {
		_ = h.services.AiOrderCancellationService.Complete(ctx, id, aidomain.ActionFailed, "executor_unavailable")
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	snapshot := orderapp.AICancelSnapshot{
		OrderID: requested.OrderID, OrderNo: requested.OrderNo, Currency: requested.Currency,
		Status: requested.ExpectedStatus, Amount: requested.ExpectedTotal,
		UpdatedAt: requested.ExpectedUpdatedAt,
	}
	order, err := h.canceller.CancelStrictlyUnpaidForAI(snapshot)
	if err != nil || order == nil {
		status := aidomain.ActionFailed
		reason := "merchant_cancel_failed"
		httpStatus := http.StatusServiceUnavailable
		if errors.Is(err, orderapp.ErrOrderCancelNotAllowed) || errors.Is(err, orderapp.ErrOrderNotFound) {
			status = aidomain.ActionConflict
			reason = "unsafe_or_changed_order"
			httpStatus = http.StatusConflict
		}
		if finishErr := h.services.AiOrderCancellationService.Complete(ctx, id, status, reason); finishErr != nil {
			cancelError(c, 503, "error.order_update_failed")
			return
		}
		if httpStatus == http.StatusConflict {
			cancelError(c, httpStatus, "error.order_cancel_not_allowed")
		} else {
			cancelError(c, httpStatus, "error.order_update_failed")
		}
		return
	}
	// If this audit write fails, the cancellation may already be committed;
	// do NOT automatically re-attempt. Show 'executing' for manual reconciliation.
	if err = h.services.AiOrderCancellationService.Complete(ctx, id, aidomain.ActionSucceeded, ""); err != nil {
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{
		"order_id": order.ID, "status": order.Status, "request_id": id,
		"approval_status":     aidomain.ActionSucceeded,
		"cancellation_method": "strict_unpaid_order_existing_domain_service",
		"refund_executed":     false,
	})
}
