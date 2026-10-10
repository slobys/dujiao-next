package aiaccessorders

import (
	"context"
	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	walletdomain "github.com/dujiao-next/internal/modules/wallet/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/gin-gonic/gin"
)

type cancelStub struct {
	calls int
	err   error
	got   orderapp.AICancelSnapshot
}

func (m *cancelStub) CancelStrictlyUnpaidForAI(s orderapp.AICancelSnapshot) (*orderdomain.Order, error) {
	m.calls++
	m.got = s
	if m.err != nil {
		return nil, m.err
	}
	return &orderdomain.Order{ID: s.OrderID, OrderNo: s.OrderNo, Status: constants.OrderStatusCanceled}, nil
}
func cancelHandlerFixture(t *testing.T) (*CancellationHandler, *aidomain.Key, *cancelStub, string) {
	t.Helper()
	svc, _, _, _ := testHandler(t)
	ctx := context.Background()
	key, _, err := svc.AiAccessService.Create(ctx, "AI-Cancel", []string{aiapp.ScopeOrderCancelRequest}, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	request, err := svc.AiOrderCancellationService.Submit(ctx, key, aiapp.OrderSnapshot{
		ID: 19, OrderNo: "AI-UNPAID-19", Currency: "CNY", Status: constants.OrderStatusPendingPayment,
		Total: "120.00", UpdatedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	spy := &cancelStub{}
	h := NewCancellationHandler(svc)
	h.canceller = spy
	return h, key, spy, request.ID
}
func serveCancel(h *CancellationHandler, path string, admin bool) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if admin {
			c.Set("admin_id", uint(9))
		}
		c.Next()
	})
	group := router.Group("/admin")
	group.GET("/ai-access/order-cancellations", h.List)
	group.POST("/ai-access/order-cancellations/:id/approve", h.Approve)
	group.POST("/ai-access/order-cancellations/:id/reject", h.Reject)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	return w
}
func TestCancelAdminRequiresHumanAndExecutesOnce(t *testing.T) {
	h, key, spy, id := cancelHandlerFixture(t)
	path := "/admin/ai-access/order-cancellations/" + id + "/approve"
	if got := serveCancel(h, path, false); got.Code != 403 {
		t.Fatalf("non admin approved: %d", got.Code)
	}
	if spy.calls != 0 {
		t.Fatal("unapproved order mutation")
	}
	response := serveCancel(h, path, true)
	if response.Code != 200 {
		t.Fatalf("admin approve: code=%d body=%s", response.Code, response.Body)
	}
	if spy.calls != 1 || spy.got.OrderID != 19 || spy.got.OrderNo != "AI-UNPAID-19" {
		t.Fatalf("wrong cancellation request %+v calls=%d", spy.got, spy.calls)
	}
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatalf("duplicate approve %d", got.Code)
	}
	if spy.calls != 1 {
		t.Fatal("same order canceled multiple times")
	}
	completed, err := h.services.AiOrderCancellationService.Owned(context.Background(), key, id)
	if err != nil || completed.Status != aidomain.ActionSucceeded {
		t.Fatalf("bad approval state %+v %v", completed, err)
	}
}
func TestCancelAdminRejectsUnsafeOrderAndNeverRetries(t *testing.T) {
	h, key, spy, id := cancelHandlerFixture(t)
	spy.err = orderapp.ErrOrderCancelNotAllowed
	path := "/admin/ai-access/order-cancellations/" + id + "/approve"
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatalf("unsafe approval HTTP=%d", got.Code)
	}
	if spy.calls != 1 {
		t.Fatal("executor not called exactly once")
	}
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatal("unsafe request can retry")
	}
	item, err := h.services.AiOrderCancellationService.Owned(context.Background(), key, id)
	if err != nil || item.Status != aidomain.ActionConflict {
		t.Fatalf("missing conflict %+v %v", item, err)
	}
}
func TestCancelAdminRevocationAndRemoteOffFailClosed(t *testing.T) {
	h, key, spy, id := cancelHandlerFixture(t)
	path := "/admin/ai-access/order-cancellations/" + id + "/approve"
	if _, err := h.services.AiRemoteService.SetConfig(context.Background(), false, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatalf("remote off approved %d", got.Code)
	}
	if spy.calls != 0 {
		t.Fatal("remote-off executed")
	}
	if _, err := h.services.AiRemoteService.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := h.services.AiAccessService.Revoke(context.Background(), key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatalf("revoked key approved %d", got.Code)
	}
	if spy.calls != 0 {
		t.Fatal("revoked proposal executed")
	}
	if got := serveCancel(h, "/admin/ai-access/order-cancellations/"+id+"/reject", true); got.Code != 409 {
		t.Fatalf("revoked request should already be rejected: %d", got.Code)
	}
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatal("rejected proposal executed")
	}
}

type refundServiceStub struct {
	calls     int
	err       error
	got       orderrefund.AIWalletRefundSnapshot
	input     orderrefund.AdminRefundToWalletInput
	requestID string
}

func (m *refundServiceStub) AdminRefundWalletForAI(input orderrefund.AdminRefundToWalletInput, snap orderrefund.AIWalletRefundSnapshot, id string) (
	*orderdomain.Order, *walletdomain.Transaction, *orderdomain.OrderRefundRecord, error) {
	m.calls++
	m.got = snap
	m.input = input
	m.requestID = id
	if m.err != nil {
		return nil, nil, nil, m.err
	}
	return &orderdomain.Order{ID: input.OrderID, Status: constants.OrderStatusPartiallyRefunded},
		&walletdomain.Transaction{ID: 11}, &orderdomain.OrderRefundRecord{ID: 12}, nil
}
func refundHandlerFixture(t *testing.T) (*WalletRefundHandler, *aidomain.Key, *refundServiceStub, string) {
	t.Helper()
	c, _, _, _ := testHandler(t)
	ctx := context.Background()
	key, _, err := c.AiAccessService.Create(ctx, "AI wallet", []string{aiapp.ScopeWalletRefundRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	record, err := c.AiWalletRefundService.Submit(ctx, key, aiapp.WalletRefundSnapshot{
		OrderID: 44, OrderNo: "AI-WALLET-44", Currency: "CNY", Status: "paid",
		Total: "100.00", Refunded: "0.00", UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}, "20.00", "customer_request")
	if err != nil {
		t.Fatal(err)
	}
	fake := &refundServiceStub{}
	h := NewWalletRefundHandler(c)
	h.refunds = fake
	return h, key, fake, record.ID
}
func serveRefund(h *WalletRefundHandler, path string, authorized bool) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if authorized {
			c.Set("admin_id", uint(7))
		}
		c.Next()
	})
	group := router.Group("/admin")
	group.POST("/ai-access/wallet-refunds/:id/approve", h.Approve)
	group.POST("/ai-access/wallet-refunds/:id/reject", h.Reject)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	return w
}
func TestWalletApprovalRequiresHumanAndCreditsOnce(t *testing.T) {
	h, key, fake, id := refundHandlerFixture(t)
	path := "/admin/ai-access/wallet-refunds/" + id + "/approve"
	if got := serveRefund(h, path, false); got.Code != 403 {
		t.Fatalf("unauthorized approval %d", got.Code)
	}
	if fake.calls != 0 {
		t.Fatal("AI submitted refund transferred money before approval")
	}
	response := serveRefund(h, path, true)
	if response.Code != 200 {
		t.Fatalf("wallet approval %d %s", response.Code, response.Body.String())
	}
	if fake.calls != 1 || fake.got.Currency != "CNY" || fake.input.Amount.String() != "20.00" ||
		fake.requestID != id {
		t.Fatalf("wrong refund details %#v %#v", fake.input, fake.got)
	}
	if got := serveRefund(h, path, true); got.Code != 409 {
		t.Fatalf("wallet refund replay %d", got.Code)
	}
	if fake.calls != 1 {
		t.Fatal("wallet credited multiple times")
	}
	state, err := h.services.AiWalletRefundService.Owned(context.Background(), key, id)
	if err != nil || state.Status != aidomain.ActionSucceeded {
		t.Fatalf("refund not recorded %+v %v", state, err)
	}
}
func TestWalletApprovalRejectsChangedOrderNoRetry(t *testing.T) {
	h, key, fake, id := refundHandlerFixture(t)
	fake.err = orderrefund.ErrAIWalletRefundUnsafe
	path := "/admin/ai-access/wallet-refunds/" + id + "/approve"
	if got := serveRefund(h, path, true); got.Code != 409 {
		t.Fatalf("stale wallet refund attempted %d", got.Code)
	}
	if fake.calls != 1 {
		t.Fatal("unexpected retries")
	}
	if got := serveRefund(h, path, true); got.Code != 409 {
		t.Fatal("conflicted request replay")
	}
	state, err := h.services.AiWalletRefundService.Owned(context.Background(), key, id)
	if err != nil || state.Status != aidomain.ActionConflict {
		t.Fatalf("missing refund conflict %+v %v", state, err)
	}
}
func TestWalletApprovalRefusesDisabledServiceOrRevokedAgent(t *testing.T) {
	h, key, fake, id := refundHandlerFixture(t)
	path := "/admin/ai-access/wallet-refunds/" + id + "/approve"
	if _, err := h.services.AiRemoteService.SetConfig(context.Background(), false, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := serveRefund(h, path, true); got.Code != 409 {
		t.Fatalf("remote disabled approved %d", got.Code)
	}
	if fake.calls != 0 {
		t.Fatal("unapproved payout")
	}
	if _, err := h.services.AiRemoteService.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := h.services.AiAccessService.Revoke(context.Background(), key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := serveRefund(h, path, true); got.Code != 409 {
		t.Fatalf("revoked AI approved %d", got.Code)
	}
	if fake.calls != 0 {
		t.Fatal("revoked payout executed")
	}
	if got := serveRefund(h, "/admin/ai-access/wallet-refunds/"+id+"/reject", true); got.Code != 409 {
		t.Fatalf("revoked refund proposal should already be rejected: %d", got.Code)
	}
}
