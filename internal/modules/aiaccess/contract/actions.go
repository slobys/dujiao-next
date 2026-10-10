package contract

import (
	"context"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"time"
)

type ActionRepository interface {
	CreateAction(context.Context, *domain.ActionRequest, *domain.Audit, string) error
	GetAction(context.Context, string) (*domain.ActionRequest, error)
	ListActions(context.Context, int) ([]domain.ActionRequest, error)
	ClaimAction(context.Context, string, uint, time.Time) (bool, error)
	RejectAction(context.Context, string, uint, time.Time) (bool, error)
	FinishAction(context.Context, string, string, string, time.Time) error
}
