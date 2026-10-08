package contract

import (
	"context"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

// RemoteRepository is separate from legacy AI key CRUD; the OAuth session
// writes are atomic and token material is never stored in plaintext.
type RemoteRepository interface {
	GetRemote(context.Context) (*domain.RemoteConfig, error)
	SaveRemote(context.Context, *domain.RemoteConfig) error
	RegisterClient(context.Context, *domain.OAuthClient) error
	FindClient(context.Context, string) (*domain.OAuthClient, error)
	CreateAuthorization(context.Context, *domain.AuthorizationRequest) error
	FindAuthorization(context.Context, string) (*domain.AuthorizationRequest, error)
	ApproveAuthorization(context.Context, string, string, string, uint, time.Time) (bool, error)
	DenyAuthorization(context.Context, string, time.Time) (bool, error)
	RedeemAuthorization(context.Context, string, string, time.Time, *domain.Key, *domain.OAuthRefresh, *domain.Audit) (bool, error)
	RefreshOAuth(context.Context, string, string, string, string, time.Time, string, string, time.Time, *domain.Audit) (bool, error)
}
