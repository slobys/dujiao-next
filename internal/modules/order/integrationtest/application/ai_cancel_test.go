package application_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"
	coupondomain "github.com/dujiao-next/internal/modules/coupon/domain"
	coupongormstore "github.com/dujiao-next/internal/modules/coupon/infrastructure/gormstore"
	fulfillmentdomain "github.com/dujiao-next/internal/modules/fulfillment/domain"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	ordergormstore "github.com/dujiao-next/internal/modules/order/infrastructure/gormstore"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func aiCancelFixture(t *testing.T, namespace string) (*gorm.DB, *orderapp.OrderService) {
	t.Helper()
	db := setupCancelPaymentTestDB(t, namespace)
	if err := db.AutoMigrate(&orderdomain.OrderRefundRecord{}); err != nil {
		t.Fatal(err)
	}
	return db, orderapp.NewOrderService(orderapp.OrderServiceOptions{
		OrderStore:       ordergormstore.New(db, "test-guest-credential-secret-with-32-bytes"),
		ProductStore:     productgormstore.NewProductStore(db),
		ProductSKUStore:  productgormstore.NewSKUStore(db),
		CouponStore:      coupongormstore.New(db),
		CouponUsageStore: coupongormstore.NewUsageStore(db),
	})
}
func newSafeUnpaidOrder(t *testing.T, db *gorm.DB) *orderdomain.Order {
	t.Helper()
	order := newPendingOrderForCancel(fmt.Sprintf("AI-SAFE-%d", time.Now().UnixNano()), 0, nil, time.Now())
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	return order
}
func aiCancelSnapshot(order *orderdomain.Order) orderapp.AICancelSnapshot {
	return orderapp.AICancelSnapshot{
		OrderID: order.ID, OrderNo: order.OrderNo, Currency: order.Currency, Status: order.Status,
		Amount: order.TotalAmount.String(), UpdatedAt: order.UpdatedAt,
	}
}
func expectAICancelDenied(t *testing.T, svc *orderapp.OrderService, source *orderdomain.Order) {
	t.Helper()
	if _, err := svc.CancelStrictlyUnpaidForAI(aiCancelSnapshot(source)); !errors.Is(err, orderapp.ErrOrderCancelNotAllowed) {
		t.Fatalf("unsafe AI cancellation was permitted or wrong error: %v", err)
	}
}
func TestAIStrictUnpaidCancellationRealTransactionAndReplay(t *testing.T) {
	db, svc := aiCancelFixture(t, "strict_allow")
	order := newSafeUnpaidOrder(t, db)
	changed, err := svc.CancelStrictlyUnpaidForAI(aiCancelSnapshot(order))
	if err != nil {
		t.Fatalf("narrow unpaid cancellation failed: %v", err)
	}
	if changed == nil || changed.Status != constants.OrderStatusCanceled || changed.CanceledAt == nil {
		t.Fatalf("did not cancel: %+v", changed)
	}
	var fromDB orderdomain.Order
	if err = db.First(&fromDB, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fromDB.Status != constants.OrderStatusCanceled || fromDB.CanceledAt == nil {
		t.Fatalf("DB unchanged: %+v", fromDB)
	}
	expectAICancelDenied(t, svc, order)
}
func TestAIStrictUnpaidCancellationRejectsPaymentsEvenFailedOrDeleted(t *testing.T) {
	for _, status := range []string{
		constants.PaymentStatusInitiated, constants.PaymentStatusPending,
		constants.PaymentStatusFailed, constants.PaymentStatusSuccess,
		constants.PaymentStatusExpired,
	} {
		t.Run(status, func(t *testing.T) {
			db, svc := aiCancelFixture(t, "paid_"+status)
			order := newSafeUnpaidOrder(t, db)
			payment := newPaymentForOrder(order.ID, status, time.Now())
			if err := db.Create(payment).Error; err != nil {
				t.Fatal(err)
			}
			if status == constants.PaymentStatusFailed {
				when := time.Now()
				if err := db.Model(payment).UpdateColumn("deleted_at", when).Error; err != nil {
					t.Fatal(err)
				}
			}
			expectAICancelDenied(t, svc, order)
			var actual orderdomain.Order
			if err := db.First(&actual, order.ID).Error; err != nil {
				t.Fatal(err)
			}
			if actual.Status != constants.OrderStatusPendingPayment {
				t.Fatal("payment-protected order modified")
			}
		})
	}
}
func TestAIStrictUnpaidCancellationRejectsMoneyCouponAndRelatedArtifacts(t *testing.T) {
	checks := []struct {
		name       string
		makeUnsafe func(*testing.T, *gorm.DB, *orderdomain.Order)
	}{
		{"wallet_paid", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			if err := db.Model(o).Update("wallet_paid_amount", money.FromDecimal(decimal.NewFromInt(2))).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"paid_at", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			if err := db.Model(o).Update("paid_at", time.Now()).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"coupon", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			id := uint(5)
			if err := db.Model(o).Update("coupon_id", id).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"refund_record", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			record := orderdomain.OrderRefundRecord{OrderID: o.ID, Type: "manual", Amount: money.FromDecimal(decimal.NewFromInt(1)), Currency: "CNY"}
			if err := db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"historic_coupon_usage", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			coupon := coupondomain.CouponUsage{OrderID: o.ID, CouponID: 200, DiscountAmount: money.FromDecimal(decimal.NewFromInt(1))}
			if err := db.Create(&coupon).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"historic_child_order", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			child := newPendingOrderForCancel("CHILD-"+o.OrderNo, 0, &o.ID, time.Now())
			if err := db.Create(child).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"fulfillment_record", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			fulfillment := fulfillmentdomain.Fulfillment{OrderID: o.ID, Type: "manual", Status: "pending"}
			if err := db.Create(&fulfillment).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"affiliate", func(t *testing.T, db *gorm.DB, o *orderdomain.Order) {
			if err := db.Model(o).Update("affiliate_code", "CODE").Error; err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, item := range checks {
		t.Run(item.name, func(t *testing.T) {
			db, svc := aiCancelFixture(t, "unsafe_"+item.name)
			order := newSafeUnpaidOrder(t, db)
			item.makeUnsafe(t, db, order)
			// For persisted monetary changes, reload and ask for the CURRENT
			// snapshot so the test proves the safety predicate, not just stale time.
			var latest orderdomain.Order
			if err := db.First(&latest, order.ID).Error; err != nil {
				t.Fatal(err)
			}
			expectAICancelDenied(t, svc, &latest)
		})
	}
}
func TestAIStrictUnpaidCancellationRejectsStaleOrChangedSnapshot(t *testing.T) {
	db, svc := aiCancelFixture(t, "stale")
	order := newSafeUnpaidOrder(t, db)
	for _, mutate := range []func(*orderapp.AICancelSnapshot){
		func(v *orderapp.AICancelSnapshot) { v.OrderNo = "DIFFERENT" },
		func(v *orderapp.AICancelSnapshot) { v.Currency = "USD" },
		func(v *orderapp.AICancelSnapshot) { v.Amount = "1.00" },
		func(v *orderapp.AICancelSnapshot) { v.UpdatedAt = v.UpdatedAt.Add(time.Hour) },
	} {
		snapshot := aiCancelSnapshot(order)
		mutate(&snapshot)
		if _, err := svc.CancelStrictlyUnpaidForAI(snapshot); !errors.Is(err, orderapp.ErrOrderCancelNotAllowed) {
			t.Fatalf("stale snapshot authorized cancellation: %v", err)
		}
	}
	var updated orderdomain.Order
	if err := db.First(&updated, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != constants.OrderStatusPendingPayment {
		t.Fatal("stale guard modified order")
	}
}
