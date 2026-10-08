package aiaccessorders

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/app/container"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	aistore "github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	ordercontract "github.com/dujiao-next/internal/modules/order/contract"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type orderGetterStub struct {
	ordercontract.Store
	order *orderdomain.Order
}

func (s *orderGetterStub) GetByID(id uint) (*orderdomain.Order, error) {
	if id != s.order.ID {
		return nil, nil
	}
	return s.order, nil
}
func testHandler(t *testing.T) (*container.Container, *aidomain.Key, *orderGetterStub, *aiapp.OrderReviews) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:order_review_admin_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&aidomain.Key{}, &aidomain.Audit{}, &aidomain.OAuthRefresh{}, &aidomain.RemoteConfig{}, &aidomain.OrderReview{}, &aidomain.ActionRequest{}, &aidomain.OrderCancellation{}); err != nil {
		t.Fatal(err)
	}
	store := aistore.New(db)
	keys := aiapp.New(store)
	remote := aiapp.NewRemote(store, keys)
	if _, err = remote.SetConfig(context.Background(), true, "https://shop.example.com"); err != nil {
		t.Fatal(err)
	}
	reviews := aiapp.NewOrderReviews(store)
	key, _, err := keys.Create(context.Background(), "Operator", []string{aiapp.ScopeOrderReviewRequest}, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	order := &orderGetterStub{order: &orderdomain.Order{
		ID: 17, OrderNo: "TEST-ORDER", Status: "paid", Currency: "CNY",
		TotalAmount: money.FromDecimal(decimal.RequireFromString("42.00")),
		UpdatedAt:   now, CreatedAt: now.Add(-time.Hour),
		GuestEmail: "SENSITIVE@example.test",
	}}
	return &container.Container{
		AiAccessService: keys, AiRemoteService: remote, AiOrderReviewService: reviews, AiOrderCancellationService:aiapp.NewOrderCancellations(store),OrderStore: order,
	}, key, order, reviews
}
func orderRequest(t *testing.T, c *container.Container, path string, method string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(ctx *gin.Context) {
		if admin {
			ctx.Set("admin_id", uint(8))
		}
		ctx.Next()
	})
	RegisterAdminRoutes(r.Group("/admin"), c)
	request := httptest.NewRequest(method, path, strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	return w
}
func submitReview(t *testing.T, svc *aiapp.OrderReviews, key *aidomain.Key, store *orderGetterStub) string {
	t.Helper()
	o := store.order
	item, err := svc.Submit(context.Background(), key, aiapp.OrderSnapshot{
		ID: o.ID, OrderNo: o.OrderNo, Status: o.Status, Total: o.TotalAmount.String(),
		Currency: o.Currency, UpdatedAt: o.UpdatedAt,
	}, "refund_review")
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}
func TestAcceptThenResolveOnlyChangesTicket(t *testing.T) {
	c, key, order, reviews := testHandler(t)
	id := submitReview(t, reviews, key, order)
	approve := "/admin/ai-access/order-reviews/" + id + "/accept"
	if res := orderRequest(t, c, approve, http.MethodPost, false); res.Code != 403 {
		t.Fatalf("non-admin approved: %d", res.Code)
	}
	res := orderRequest(t, c, approve, http.MethodPost, true)
	if res.Code != 200 {
		t.Fatalf("accept status %d body %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "SENSITIVE@example.test") {
		t.Fatal("PII in response")
	}
	if order.order.Status != "paid" {
		t.Fatal("accepting ticket performed a refund/cancellation")
	}
	if res := orderRequest(t, c, approve, http.MethodPost, true); res.Code != 409 {
		t.Fatalf("repeated acceptance: %d", res.Code)
	}
	resolved := "/admin/ai-access/order-reviews/" + id + "/resolve"
	if res := orderRequest(t, c, resolved, http.MethodPost, true); res.Code != 200 {
		t.Fatalf("resolve failed %d", res.Code)
	}
	if res := orderRequest(t, c, resolved, http.MethodPost, true); res.Code != 409 {
		t.Fatal("duplicate resolution")
	}
	item, err := reviews.GetForAdmin(context.Background(), id)
	if err != nil || item.Status != aidomain.OrderReviewResolved {
		t.Fatalf("unexpected final ticket %+v %v", item, err)
	}
	if order.order.Status != "paid" {
		t.Fatal("resolve modified order")
	}
}
func TestChangedOrderCannotReceiveStaleTicketApproval(t *testing.T) {
	c, key, order, reviews := testHandler(t)
	id := submitReview(t, reviews, key, order)
	order.order.Status = "refunded"
	target := "/admin/ai-access/order-reviews/" + id + "/accept"
	res := orderRequest(t, c, target, http.MethodPost, true)
	if res.Code != 409 {
		t.Fatalf("stale order accepted: %d", res.Code)
	}
	item, err := reviews.GetForAdmin(context.Background(), id)
	if err != nil || item.Status != aidomain.OrderReviewConflict {
		t.Fatalf("missing stale marker %+v %v", item, err)
	}
	if res = orderRequest(t, c, target, http.MethodPost, true); res.Code != 409 {
		t.Fatalf("conflicted request recycled: %d", res.Code)
	}
}
func TestRejectRevokedTicketWithoutAnyOrderMutation(t *testing.T) {
	c, key, order, reviews := testHandler(t)
	id := submitReview(t, reviews, key, order)
	if err := c.AiAccessService.Revoke(context.Background(), key.ID, 1); err != nil {
		t.Fatal(err)
	}
	accept := "/admin/ai-access/order-reviews/" + id + "/accept"
	if got := orderRequest(t, c, accept, http.MethodPost, true); got.Code != 409 {
		t.Fatalf("revoked AI ticket accepted %d", got.Code)
	}
	if got := orderRequest(t, c, "/admin/ai-access/order-reviews/"+id+"/reject", http.MethodPost, true); got.Code != 200 {
		t.Fatalf("could not close revoked ticket %d", got.Code)
	}
	if order.order.Status != "paid" {
		t.Fatal("ticket changed paid order")
	}
}
