package aiaccessorders

import (
	"errors"
	"net/http"

	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	walletdomain "github.com/dujiao-next/internal/modules/wallet/domain"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/shopspring/decimal"

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

type narrowWalletRefunder interface {
	AdminRefundWalletForAI(orderrefund.AdminRefundToWalletInput, orderrefund.AIWalletRefundSnapshot, string) (
		*orderdomain.Order, *walletdomain.Transaction, *orderdomain.OrderRefundRecord, error)
}
type WalletRefundHandler struct {
	services *container.Container
	refunds  narrowWalletRefunder
}

func NewWalletRefundHandler(c *container.Container) *WalletRefundHandler {
	return &WalletRefundHandler{services: c, refunds: c.OrderRefundService}
}
func RegisterWalletRefundRoutes(routes gin.IRoutes, c *container.Container) {
	h := NewWalletRefundHandler(c)
	routes.GET("/ai-access/wallet-refunds", h.List)
	routes.POST("/ai-access/wallet-refunds/:id/approve", h.Approve)
	routes.POST("/ai-access/wallet-refunds/:id/reject", h.Reject)
}
func (h *WalletRefundHandler) List(c *gin.Context) {
	items, err := h.services.AiWalletRefundService.List(c.Request.Context(), 100)
	if err != nil {
		cancelError(c, 503, "error.order_fetch_failed")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, items)
}
func (h *WalletRefundHandler) Reject(c *gin.Context) {
	if adminID(c) == 0 {
		cancelError(c, 403, "error.unauthorized")
		return
	}
	err := h.services.AiWalletRefundService.Reject(c.Request.Context(), c.Param("id"), adminID(c))
	if err != nil {
		if errors.Is(err, aiapp.ErrWalletRefundUnavailable) {
			cancelError(c, 409, "error.order_status_invalid")
			return
		}
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	response.Success(c, gin.H{"status": aidomain.ActionRejected})
}
func (h *WalletRefundHandler) Approve(c *gin.Context) {
	ctx := c.Request.Context()
	if adminID(c) == 0 {
		cancelError(c, 403, "error.unauthorized")
		return
	}
	if _, err := h.services.AiRemoteService.Active(ctx); err != nil {
		cancelError(c, 409, "error.order_status_invalid")
		return
	}

	// Durable claim + audit MUST commit BEFORE any wallet funds move.
	// A crashed worker stays "executing" and needs reconciliation, never retry.
	request, err := h.services.AiWalletRefundService.Claim(ctx, c.Param("id"), adminID(c))
	if err != nil {
		if errors.Is(err, aiapp.ErrWalletRefundUnavailable) {
			cancelError(c, 409, "error.order_status_invalid")
			return
		}
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	if request == nil || h.refunds == nil {
		_ = h.services.AiWalletRefundService.Complete(ctx, c.Param("id"), aidomain.ActionFailed, "executor_unavailable")
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	amount, err := decimal.NewFromString(request.Amount)
	if err != nil || amount.Exponent() < -2 || amount.Sign() <= 0 || amount.GreaterThan(decimal.RequireFromString("500.00")) {
		_ = h.services.AiWalletRefundService.Complete(ctx, request.ID, aidomain.ActionFailed, "invalid_request_amount")
		cancelError(c, 409, "error.order_status_invalid")
		return
	}
	snap := orderrefund.AIWalletRefundSnapshot{
		OrderID: request.OrderID, OrderNo: request.OrderNo, Currency: request.Currency,
		Status: request.ExpectedStatus, TotalAmount: request.ExpectedTotal,
		RefundedAmount: request.ExpectedRefunded, UpdatedAt: request.ExpectedUpdatedAt,
	}
	order, txn, record, err := h.refunds.AdminRefundWalletForAI(orderrefund.AdminRefundToWalletInput{
		OrderID: request.OrderID, Amount: money.FromDecimal(amount),
		Remark: "AI approved wallet refund (" + request.Reason + "): " + request.ID,
	}, snap, request.ID)
	if err != nil || order == nil || txn == nil || record == nil {
		status := aidomain.ActionFailed
		reason := "wallet_refund_failed"
		httpCode := http.StatusServiceUnavailable
		if errors.Is(err, orderrefund.ErrAIWalletRefundUnsafe) {
			status = aidomain.ActionConflict
			reason = "stale_or_ineligible"
			httpCode = http.StatusConflict
		}
		if finishErr := h.services.AiWalletRefundService.Complete(ctx, request.ID, status, reason); finishErr != nil {
			cancelError(c, 503, "error.order_update_failed")
			return
		}
		cancelError(c, httpCode, "error.order_status_invalid")
		return
	}
	// Wallet balance credit and refund record already committed atomically
	// through native Refund + Wallet domain services. A failure to record the
	// result stays executing; never call money-moving code twice to recover.
	if err = h.services.AiWalletRefundService.Complete(ctx, request.ID, aidomain.ActionSucceeded, ""); err != nil {
		cancelError(c, 503, "error.order_update_failed")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{
		"request_id": request.ID, "order_id": order.ID, "status": aidomain.ActionSucceeded,
		"amount": request.Amount, "currency": request.Currency,
		"refund_target":             "registered_user_wallet_balance",
		"original_gateway_refunded": false,
		"wallet_transaction_id":     txn.ID, "refund_record_id": record.ID,
	})
}
