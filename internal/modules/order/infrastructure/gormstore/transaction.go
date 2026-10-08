package gormstore

import (
	"strings"
	"time"

	fulfillmentdomain "github.com/dujiao-next/internal/modules/fulfillment/domain"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	paymentdomain "github.com/dujiao-next/internal/modules/payment/domain"

	affiliatecontract "github.com/dujiao-next/internal/modules/affiliate/contract"
	affiliategormstore "github.com/dujiao-next/internal/modules/affiliate/infrastructure/gormstore"
	cardsecretcontract "github.com/dujiao-next/internal/modules/cardsecret/contract"
	cardsecretgormstore "github.com/dujiao-next/internal/modules/cardsecret/infrastructure/gormstore"
	productcontract "github.com/dujiao-next/internal/modules/catalog/product/contract"
	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"
	couponcontract "github.com/dujiao-next/internal/modules/coupon/contract"
	coupondomain "github.com/dujiao-next/internal/modules/coupon/domain"
	coupongormstore "github.com/dujiao-next/internal/modules/coupon/infrastructure/gormstore"
	fulfillmentcontract "github.com/dujiao-next/internal/modules/fulfillment/contract"
	fulfillmentgormstore "github.com/dujiao-next/internal/modules/fulfillment/infrastructure/gormstore"
	ordercontract "github.com/dujiao-next/internal/modules/order/contract"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
	resellergormstore "github.com/dujiao-next/internal/modules/reseller/infrastructure/gormstore"
	walletcontract "github.com/dujiao-next/internal/modules/wallet/contract"
	walletgormstore "github.com/dujiao-next/internal/modules/wallet/infrastructure/gormstore"

	"github.com/dujiao-next/internal/constants"

	"gorm.io/gorm"
)

type transaction struct {
	db                    *gorm.DB
	guestCredentialSecret []byte
}

var _ ordercontract.Transaction = transaction{}

// UseTransaction 把调用方已打开的数据库事务适配为订单工作单元。
// 跨领域工作流通过该入口保持同库原子性，同时不把 GORM 暴露给订单应用层。
func UseTransaction(tx *gorm.DB, guestCredentialSecret string) ordercontract.Transaction {
	if tx == nil {
		return nil
	}
	secret := []byte(strings.TrimSpace(guestCredentialSecret))
	if len(secret) == 0 {
		panic("order transaction: guest credential secret is required")
	}
	return useTransaction(tx, secret)
}

func useTransaction(tx *gorm.DB, guestCredentialSecret []byte) ordercontract.Transaction {
	if tx == nil {
		return nil
	}
	return transaction{db: tx, guestCredentialSecret: guestCredentialSecret}
}

func (tx transaction) Orders() ordercontract.Store {
	return &Store{db: tx.db, guestCredentialSecret: tx.guestCredentialSecret}
}

func (tx transaction) Products() productcontract.Repository {
	return productgormstore.NewProductStore(tx.db)
}

func (tx transaction) ProductSKUs() productcontract.SKURepository {
	return productgormstore.NewSKUStore(tx.db)
}

func (tx transaction) CardSecrets() cardsecretcontract.Repository {
	return cardsecretgormstore.New(tx.db)
}

func (tx transaction) Coupons() couponcontract.Repository {
	return coupongormstore.New(tx.db)
}

func (tx transaction) CouponUsages() couponcontract.UsageRepository {
	return coupongormstore.NewUsageStore(tx.db)
}

func (tx transaction) Fulfillments() fulfillmentcontract.Store {
	return fulfillmentgormstore.New(tx.db)
}

func (tx transaction) Wallets() walletcontract.Transaction {
	return walletgormstore.UseTransaction(tx.db)
}

func (tx transaction) Affiliates() affiliatecontract.Store {
	return affiliategormstore.New(tx.db)
}

func (tx transaction) ResellerOrders() ordercontract.ResellerOrderStore {
	return resellergormstore.New(tx.db)
}

func (tx transaction) ResellerAccounting() resellercontract.AccountingLedgerStore {
	return resellergormstore.New(tx.db)
}

// HasAnySettlementArtifactsForOrder is used ONLY for approved AI strict
// unpaid cancellation. Query through the transaction handle (SQLite safe).
// Any payment, refund, fulfillment, coupon usage or child order,
// including soft-deleted records, blocks the action. Missing tables fail closed.
func (tx transaction) HasAnySettlementArtifactsForOrder(orderID uint) (bool, error) {
	if orderID == 0 {
		return true, nil
	}
	for _, model := range []interface{}{
		&paymentdomain.Payment{}, &orderdomain.OrderRefundRecord{}, &fulfillmentdomain.Fulfillment{}, &coupondomain.CouponUsage{},
	} {
		var count int64
		if err := tx.db.Unscoped().Model(model).Where("order_id = ?", orderID).Count(&count).Error; err != nil {
			return true, err
		}
		if count > 0 {
			return true, nil
		}
	}
	// Even a soft-deleted child or coupon usage makes this a complex order.
	var childCount int64
	if err := tx.db.Unscoped().Model(&orderdomain.Order{}).Where("parent_id = ?", orderID).Count(&childCount).Error; err != nil {
		return true, err
	}
	if childCount > 0 {
		return true, nil
	}
	return false, nil
}

func (tx transaction) ExpirePendingPaymentsByOrderIDs(orderIDs []uint, expiredAt time.Time) (int64, error) {
	if len(orderIDs) == 0 {
		return 0, nil
	}
	result := tx.db.Model(&paymentdomain.Payment{}).
		Where("deleted_at IS NULL AND order_id IN ? AND status IN ?", orderIDs, []string{constants.PaymentStatusInitiated, constants.PaymentStatusPending}).
		Updates(map[string]interface{}{
			"status":     constants.PaymentStatusExpired,
			"expired_at": expiredAt,
			"updated_at": expiredAt,
		})
	return result.RowsAffected, result.Error
}

func (s *Store) WithinTransaction(fn func(ordercontract.Transaction) error) error {
	if fn == nil {
		return nil
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		return fn(useTransaction(tx, s.guestCredentialSecret))
	})
}
