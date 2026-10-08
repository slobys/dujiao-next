package refund

import (
	"errors"
	"fmt"
	"strings"
	"time"

	orderapp "github.com/dujiao-next/internal/modules/order/application"

	ordercontract "github.com/dujiao-next/internal/modules/order/contract"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"

	"github.com/dujiao-next/internal/constants"
	settingsapp "github.com/dujiao-next/internal/modules/settings/application"
	walletcontract "github.com/dujiao-next/internal/modules/wallet/contract"
	walletdomain "github.com/dujiao-next/internal/modules/wallet/domain"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

// AdminRefundToWalletInput describes an order refund whose settlement target
// is the registered user's wallet.
type AdminRefundToWalletInput struct {
	OrderID uint
	Amount  money.Amount
	Remark  string
}

// All AI wallet refund eligibility checks MUST share the locked order transaction.
// If the selected store cannot query historical online payment attempts from
// the same transaction, the money-moving operation fails closed.
type aiWalletPaymentInspector interface {
	HasAnyPaymentAttemptsForWalletRefund(uint) (bool, error)
}

// AIWalletRefundSnapshot is an immutable human-review snapshot. It is never
// passed to the model in full; only sanitized monetary fields are exposed.
// AI wallet credit is permitted only for an originally ALL-WALLET-paid,
// registered, standalone order. Online/provider refunds are excluded.
type AIWalletRefundSnapshot struct {
	OrderID        uint
	OrderNo        string
	Currency       string
	Status         string
	TotalAmount    string
	RefundedAmount string
	UpdatedAt      time.Time
}

var ErrAIWalletRefundUnsafe = errors.New("AI wallet refund preconditions failed")

// AIWalletRefundEligible is a conservative merchant-level preflight.
// The same checks run again on a locked order inside the credit transaction.
func AIWalletRefundEligible(order *orderdomain.Order, snap AIWalletRefundSnapshot, refund money.Amount) bool {
	if order == nil || order.ID == 0 || order.ID != snap.OrderID ||
		order.OrderNo != snap.OrderNo || order.Currency != "CNY" || order.Currency != snap.Currency ||
		order.Status != snap.Status || order.UpdatedAt.IsZero() ||
		!order.UpdatedAt.Equal(snap.UpdatedAt) || order.TotalAmount.String() != snap.TotalAmount ||
		order.RefundedAmount.String() != snap.RefundedAmount ||
		order.UserID == 0 || order.PaidAt == nil || order.ParentID != nil || len(order.Children) != 0 ||
		order.CouponID != nil || order.AffiliateProfileID != nil || order.AffiliateCode != "" ||
		order.ResellerID != nil || order.ResellerDomain != "" ||
		order.OnlinePaidAmount.Decimal.Sign() != 0 ||
		!order.WalletPaidAmount.Decimal.Round(2).Equal(order.TotalAmount.Decimal.Round(2)) ||
		order.TotalAmount.Decimal.Sign() <= 0 ||
		order.RefundedAmount.Decimal.IsNegative() {
		return false
	}
	switch order.Status {
	case constants.OrderStatusPaid, constants.OrderStatusFulfilling,
		constants.OrderStatusDelivered, constants.OrderStatusCompleted,
		constants.OrderStatusPartiallyRefunded:
	default:
		return false
	}
	max := decimal.RequireFromString("500.00")
	remaining := order.TotalAmount.Decimal.Sub(order.RefundedAmount.Decimal).Round(2)
	amount := refund.Decimal
	if amount.Exponent() < -2 || amount.Sign() <= 0 || amount.GreaterThan(max) || amount.GreaterThan(remaining) {
		return false
	}
	return true
}

// AdminRefundWalletForAI uses the existing native wallet credit workflow and
// protects it with the EXACT approved snapshot INSIDE the same transaction.
// The request ID generates a stable wallet accounting reference for manual
// crash reconciliation. Approval/replay protection occurs in AI queue first.
func (s *Service) AdminRefundWalletForAI(
	input AdminRefundToWalletInput, snap AIWalletRefundSnapshot, requestID string,
) (*orderdomain.Order, *walletdomain.Transaction, *orderdomain.OrderRefundRecord, error) {
	if len(requestID) != 32 {
		return nil, nil, nil, ErrAIWalletRefundUnsafe
	}
	for _, c := range requestID {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, nil, nil, ErrAIWalletRefundUnsafe
		}
	}
	if input.OrderID != snap.OrderID {
		return nil, nil, nil, ErrAIWalletRefundUnsafe
	}
	return s.adminRefundToWallet(input, &snap, fmt.Sprintf("order:%d:ai_wallet_refund:%s", input.OrderID, requestID))
}

// AdminRefundToWallet executes the order refund workflow and delegates only
// the account credit to Wallet. Order state, refund records, affiliate
// reversals, and reseller accounting remain owned by the order context.
func (s *Service) AdminRefundToWallet(input AdminRefundToWalletInput) (*orderdomain.Order, *walletdomain.Transaction, *orderdomain.OrderRefundRecord, error) {
	return s.adminRefundToWallet(input, nil, "")
}
func (s *Service) adminRefundToWallet(input AdminRefundToWalletInput, snap *AIWalletRefundSnapshot, ref string) (*orderdomain.Order, *walletdomain.Transaction, *orderdomain.OrderRefundRecord, error) {
	if input.OrderID == 0 {
		return nil, nil, nil, ErrOrderNotFound
	}
	amount := input.Amount.Decimal.Round(2)
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, nil, nil, walletcontract.ErrInvalidAmount
	}
	if s == nil || s.wallets == nil {
		return nil, nil, nil, walletcontract.ErrAccountNotFound
	}

	remark := strings.TrimSpace(input.Remark)
	walletRemark := remark
	if walletRemark == "" {
		walletRemark = "管理员退款到余额"
	}
	reference := fmt.Sprintf("order:%d:admin_refund:%d", input.OrderID, time.Now().UnixNano())
	if ref != "" {
		reference = ref
	}
	var (
		transactionResult  *walletdomain.Transaction
		refundRecordResult *orderdomain.OrderRefundRecord
	)

	config := settingsapp.DefaultOrderRefundConfig()
	if s.settingService != nil {
		loaded, err := s.settingService.GetOrderRefundConfig()
		if err != nil {
			return nil, nil, nil, err
		}
		config = loaded
	}

	err := s.orderStore.WithinTransaction(func(tx ordercontract.Transaction) error {
		orderRepository := tx.Orders()
		var locked *orderdomain.Order
		var err error
		if snap != nil {
			locked, err = orderRepository.GetByIDForUpdateWithChildren(input.OrderID)
		} else {
			locked, err = orderRepository.GetByIDForUpdate(input.OrderID)
		}
		if err != nil {
			return err
		}
		if locked == nil {
			return ErrOrderNotFound
		}
		order := *locked
		if snap != nil && !AIWalletRefundEligible(&order, *snap, input.Amount) {
			return ErrAIWalletRefundUnsafe
		}
		if snap != nil {
			inspector, ok := tx.(aiWalletPaymentInspector)
			if !ok {
				return ErrAIWalletRefundUnsafe
			}
			exists, err := inspector.HasAnyPaymentAttemptsForWalletRefund(order.ID)
			if err != nil || exists {
				return ErrAIWalletRefundUnsafe
			}
		}
		if order.UserID == 0 {
			return walletcontract.ErrNotSupportedForGuest
		}
		if order.PaidAt == nil {
			return ErrOrderStatusInvalid
		}
		now := time.Now()
		if settingsapp.IsOrderRefundWindowExpired(order.CreatedAt, order.PaidAt, config.MaxRefundDays, now) {
			return ErrOrderRefundExpired
		}
		if order.TotalAmount.Decimal.LessThanOrEqual(decimal.Zero) {
			return ErrOrderStatusInvalid
		}
		refundedBefore := order.RefundedAmount.Decimal.Round(2)
		refundable := order.TotalAmount.Decimal.Sub(refundedBefore).Round(2)
		if amount.GreaterThan(refundable) {
			return walletcontract.ErrRefundExceeded
		}

		_, transaction, err := s.wallets.CreditInTransaction(
			tx.Wallets(),
			walletcontract.CreditInput{
				UserID:    order.UserID,
				Amount:    money.FromDecimal(amount),
				Currency:  order.Currency,
				Type:      constants.WalletTxnTypeAdminRefund,
				Reference: reference,
				Remark:    walletRemark,
				OrderID:   &order.ID,
			},
		)
		if err != nil {
			return err
		}

		newRefunded := refundedBefore.Add(amount).Round(2)
		updates := map[string]interface{}{
			"refunded_amount": money.FromDecimal(newRefunded),
			"updated_at":      now,
		}
		markRefunded := newRefunded.GreaterThanOrEqual(order.TotalAmount.Decimal.Round(2))
		if markRefunded {
			updates["status"] = constants.OrderStatusRefunded
		} else {
			updates["status"] = constants.OrderStatusPartiallyRefunded
		}
		if err := orderRepository.UpdateFields(order.ID, updates); err != nil {
			return ErrOrderUpdateFailed
		}
		if order.ParentID == nil {
			targetStatus := constants.OrderStatusPartiallyRefunded
			if markRefunded {
				targetStatus = constants.OrderStatusRefunded
			}
			if err := applyParentRefundChildStatusUpdates(orderRepository, order.ID, targetStatus, now); err != nil {
				return ErrOrderUpdateFailed
			}
		} else if _, err := orderapp.SyncParentStatus(orderRepository, *order.ParentID, now); err != nil {
			return ErrOrderUpdateFailed
		}
		if s.affiliateRefund != nil {
			if err := s.affiliateRefund.HandleOrderRefunded(
				tx.Affiliates(),
				&order,
				amount,
				refundedBefore,
				"order_refunded_to_wallet",
			); err != nil {
				return err
			}
		}

		currency := strings.ToUpper(strings.TrimSpace(order.Currency))
		if currency == "" {
			currency = "CNY"
		}
		record := &orderdomain.OrderRefundRecord{
			UserID:                   order.UserID,
			GuestEmail:               order.GuestEmail,
			OrderID:                  order.ID,
			Type:                     constants.OrderRefundTypeWallet,
			Amount:                   money.FromDecimal(amount),
			PaymentFeeRefunded:       false,
			PaymentFeeRefundedAmount: money.FromDecimal(decimal.Zero),
			Currency:                 currency,
			Remark:                   remark,
			CreatedAt:                now,
			UpdatedAt:                now,
		}
		if err := orderRepository.CreateRefundRecord(record); err != nil {
			return ErrRefundRecordCreateFailed
		}
		if s.resellerAccounting != nil {
			if err := s.resellerAccounting.HandleRefundDeduct(tx.ResellerAccounting(), &order, record, refundedBefore); err != nil {
				return err
			}
		}
		transactionResult = transaction
		refundRecordResult = record
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}

	order, err := s.orderStore.GetByID(input.OrderID)
	if err != nil {
		return nil, nil, nil, ErrOrderFetchFailed
	}
	if order == nil {
		return nil, nil, nil, ErrOrderNotFound
	}
	return order, transactionResult, refundRecordResult, nil
}
