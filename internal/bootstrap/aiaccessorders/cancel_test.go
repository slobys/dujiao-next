package aiaccessorders

import (
	"context"
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
	if got := serveCancel(h, "/admin/ai-access/order-cancellations/"+id+"/reject", true); got.Code != 200 {
		t.Fatalf("could not reject orphan request: %d", got.Code)
	}
	if got := serveCancel(h, path, true); got.Code != 409 {
		t.Fatal("rejected proposal executed")
	}
}
