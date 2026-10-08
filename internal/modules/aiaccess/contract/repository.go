package contract

import (
	"context"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"time"
)

type Repository interface {
	CreateWithAudit(context.Context, *domain.Key, *domain.Audit) error
	GetByID(context.Context, uint) (*domain.Key, error)
	FindKey(context.Context, string) (*domain.Key, error)
	List(context.Context) ([]domain.Key, error)
	RotateWithAudit(context.Context, uint, string, time.Time, *domain.Audit) (bool, error)
	RevokeWithAudit(context.Context, uint, time.Time, *domain.Audit) (bool, error)
	Touch(context.Context, uint, time.Time) error
	Audit(context.Context, *domain.Audit) error
	Audits(context.Context, int) ([]domain.Audit, error)
}
