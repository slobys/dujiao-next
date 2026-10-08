package refund_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	paymentdomain "github.com/dujiao-next/internal/modules/payment/domain"
	walletdomain "github.com/dujiao-next/internal/modules/wallet/domain"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func aiWalletFixture(t *testing.T, userID uint, total decimal.Decimal) (*orderrefund.Service, *gorm.DB, *orderdomain.Order, orderrefund.AIWalletRefundSnapshot) {
	t.Helper()
	svc, db := setupOrderRefundWalletTest(t)
	createTestUser(t, db, userID)
	order := createTestOrder(t, db, userID, fmt.Sprintf("AI-WALLET-%d-%d", userID, time.Now().UnixNano()), total)
	paid := time.Now()
	if err := db.Model(&orderdomain.Order{}).Where("id = ?", order.ID).Updates(map[string]any{
		"status": constants.OrderStatusPaid, "paid_at": paid,
		"wallet_paid_amount": money.FromDecimal(total),
		"online_paid_amount": money.FromDecimal(decimal.Zero),
	}).Error; err != nil {
		t.Fatal(err)
	}
	var original orderdomain.Order
	if err := db.First(&original, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	snap := orderrefund.AIWalletRefundSnapshot{
		OrderID: original.ID, OrderNo: original.OrderNo, Currency: original.Currency,
		Status: original.Status, TotalAmount: original.TotalAmount.String(),
		RefundedAmount: original.RefundedAmount.String(), UpdatedAt: original.UpdatedAt,
	}
	return svc, db, &original, snap
}
func TestAIWalletRefundCreditsOnceAndUsesNativeLedger(t *testing.T) {
	svc, db, order, snap := aiWalletFixture(t, 468, decimal.NewFromInt(80))
	amount := money.FromDecimal(decimal.RequireFromString("20.00"))
	if !orderrefund.AIWalletRefundEligible(order, snap, amount) {
		t.Fatal("strict wallet-paid order ineligible")
	}
	requestID := "0123456789abcdef0123456789abcdef"
	result, transaction, record, err := svc.AdminRefundWalletForAI(orderrefund.AdminRefundToWalletInput{
		OrderID: order.ID, Amount: amount, Remark: "Human-approved AI wallet refund",
	}, snap, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || transaction == nil || record == nil || record.ID == 0 {
		t.Fatal("no durable refund record")
	}
	if result.Status != constants.OrderStatusPartiallyRefunded || !result.RefundedAmount.Decimal.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("refund accounting wrong %+v", result)
	}
	expectedRef := fmt.Sprintf("order:%d:ai_wallet_refund:%s", order.ID, requestID)
	if transaction.Reference != expectedRef {
		t.Fatalf("wrong reconciliation ref %s", transaction.Reference)
	}
	wallet, err := walletServiceForTest(db).GetAccount(order.UserID)
	if err != nil || !wallet.Balance.Decimal.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("wallet not credited correctly %+v %v", wallet, err)
	}
	_, _, _, err = svc.AdminRefundWalletForAI(orderrefund.AdminRefundToWalletInput{OrderID: order.ID, Amount: amount}, snap, requestID)
	if !errors.Is(err, orderrefund.ErrAIWalletRefundUnsafe) {
		t.Fatalf("stale approved request allowed second credit: %v", err)
	}
	wallet, err = walletServiceForTest(db).GetAccount(order.UserID)
	if err != nil || !wallet.Balance.Decimal.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("double wallet credit %+v %v", wallet, err)
	}
	var count int64
	if err = db.Model(&walletdomain.Transaction{}).Where("reference=?", expectedRef).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("multiple wallet ledger records %d %v", count, err)
	}
}
func TestAIWalletRefundRejectsUnsafePaymentAndStatus(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*orderdomain.Order)
	}{
		{"online_payment", func(o *orderdomain.Order) { o.OnlinePaidAmount = money.FromDecimal(decimal.NewFromInt(1)) }},
		{"partial_wallet_payment", func(o *orderdomain.Order) { o.WalletPaidAmount = money.FromDecimal(decimal.NewFromInt(79)) }},
		{"guest", func(o *orderdomain.Order) { o.UserID = 0 }},
		{"unpaid", func(o *orderdomain.Order) { o.PaidAt = nil }},
		{"pending", func(o *orderdomain.Order) { o.Status = constants.OrderStatusPendingPayment }},
		{"canceled", func(o *orderdomain.Order) { o.Status = constants.OrderStatusCanceled }},
		{"other_currency", func(o *orderdomain.Order) { o.Currency = "USD" }},
		{"affiliate", func(o *orderdomain.Order) { o.AffiliateCode = "BAD" }},
		{"reseller", func(o *orderdomain.Order) { id := uint(1); o.ResellerID = &id }},
		{"coupon", func(o *orderdomain.Order) { id := uint(1); o.CouponID = &id }},
	}
	for _, caseItem := range cases {
		t.Run(caseItem.name, func(t *testing.T) {
			_, _, order, snap := aiWalletFixture(t, 470, decimal.NewFromInt(80))
			caseItem.mutate(order)
			if orderrefund.AIWalletRefundEligible(order, snap, money.FromDecimal(decimal.NewFromInt(2))) {
				t.Fatal("unsafe wallet refund eligible")
			}
		})
	}
}
func TestAIWalletRefundRejectsExcessAmountAndStaleApproval(t *testing.T) {
	_, _, order, snap := aiWalletFixture(t, 472, decimal.NewFromInt(600))
	for _, raw := range []string{"0.00", "0.001", "500.01", "600.00", "-1.00"} {
		amount, err := decimal.NewFromString(raw)
		if err != nil {
			t.Fatal(err)
		}
		if orderrefund.AIWalletRefundEligible(order, snap, money.FromDecimal(amount)) {
			t.Fatalf("bad amount permitted: %s", raw)
		}
	}
	valid := money.FromDecimal(decimal.NewFromInt(20))
	for _, mutate := range []func(*orderrefund.AIWalletRefundSnapshot){
		func(v *orderrefund.AIWalletRefundSnapshot) { v.OrderNo = "OTHER" },
		func(v *orderrefund.AIWalletRefundSnapshot) { v.Currency = "USD" },
		func(v *orderrefund.AIWalletRefundSnapshot) { v.TotalAmount = "1.00" },
		func(v *orderrefund.AIWalletRefundSnapshot) { v.RefundedAmount = "10.00" },
		func(v *orderrefund.AIWalletRefundSnapshot) { v.UpdatedAt = v.UpdatedAt.Add(time.Second) },
	} {
		changed := snap
		mutate(&changed)
		if orderrefund.AIWalletRefundEligible(order, changed, valid) {
			t.Fatalf("stale refund snapshot eligible: %+v", changed)
		}
	}
}

func TestAIWalletRefundRejectsAnyHistoricalProviderPayment(t *testing.T) {
	for _, status := range []string{constants.PaymentStatusPending, constants.PaymentStatusFailed, constants.PaymentStatusSuccess, constants.PaymentStatusExpired} {
		t.Run(status, func(t *testing.T) {
			svc, db, order, snap := aiWalletFixture(t, 900, decimal.NewFromInt(60))
			record := paymentdomain.Payment{
				OrderID: order.ID, ChannelID: 1, ProviderType: constants.PaymentProviderEpay,
				ChannelType: "alipay", InteractionMode: constants.PaymentInteractionQR,
				Amount: money.FromDecimal(decimal.NewFromInt(60)), Currency: "CNY",
				Status: status, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}
			if err := db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			if status == constants.PaymentStatusFailed {
				if err := db.Model(&record).Update("deleted_at", time.Now()).Error; err != nil {
					t.Fatal(err)
				}
			}
			input := orderrefund.AdminRefundToWalletInput{
				OrderID: order.ID, Amount: money.FromDecimal(decimal.NewFromInt(10)),
			}
			_, _, _, err := svc.AdminRefundWalletForAI(input, snap, "1234567890abcdef1234567890abcdef")
			if !errors.Is(err, orderrefund.ErrAIWalletRefundUnsafe) {
				t.Fatalf("historical payment status %s permitted wallet refund: %v", status, err)
			}
			var credits int64
			if err := db.Model(&walletdomain.Transaction{}).Where("order_id = ? AND type = ?", order.ID, constants.WalletTxnTypeAdminRefund).Count(&credits).Error; err != nil || credits != 0 {
				t.Fatalf("money credited with payment history %d %v", credits, err)
			}
		})
	}
}
