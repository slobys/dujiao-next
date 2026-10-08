package contract

import (
	"context"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"time"
)

type WalletRefundRepository interface {
	CreateWalletRefund(context.Context, *domain.WalletRefundRequest, *domain.Audit, string) error
	GetWalletRefund(context.Context, string) (*domain.WalletRefundRequest, error)
	ListWalletRefunds(context.Context, int) ([]domain.WalletRefundRequest, error)
	ClaimWalletRefund(context.Context, string, uint, time.Time) (bool, error)
	RejectWalletRefund(context.Context, string, uint, time.Time) (bool, error)
	FinishWalletRefund(context.Context, string, string, string, time.Time) error
}
