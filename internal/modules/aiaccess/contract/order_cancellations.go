package contract

import (
	"context"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"time"
)

type OrderCancellationRepository interface {
	CreateOrderCancellation(context.Context, *domain.OrderCancellation, *domain.Audit, string) error
	GetOrderCancellation(context.Context, string) (*domain.OrderCancellation, error)
	ListOrderCancellations(context.Context, int) ([]domain.OrderCancellation, error)
	ClaimOrderCancellation(context.Context, string, uint, time.Time) (bool, error)
	RejectOrderCancellation(context.Context, string, uint, time.Time) (bool, error)
	FinishOrderCancellation(context.Context, string, string, string, time.Time) error
}
