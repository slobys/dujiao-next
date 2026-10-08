package contract

import (
	"context"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"time"
)

type OrderReviewRepository interface {
	CreateOrderReview(context.Context, *domain.OrderReview, *domain.Audit) error
	GetOrderReview(context.Context, string) (*domain.OrderReview, error)
	ListOrderReviews(context.Context, int) ([]domain.OrderReview, error)
	ProcessOrderReview(context.Context, string, uint, string, time.Time) (string, error)
}
