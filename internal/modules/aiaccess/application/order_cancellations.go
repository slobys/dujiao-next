package application

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/modules/aiaccess/contract"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

var ErrOrderCancellationUnavailable = errors.New("AI order cancellation request unavailable")

const OrderCancellationTTL = time.Hour

// OrderCancellations holds the proposal lifecycle only. The order module
// performs all real merchant cancellation logic when a human approves.
type OrderCancellations struct {
	repo contract.OrderCancellationRepository
	now  func() time.Time
}

func NewOrderCancellations(repo contract.OrderCancellationRepository) *OrderCancellations {
	return &OrderCancellations{repo: repo, now: time.Now}
}
func (s *OrderCancellations) Now() time.Time { return s.now().UTC() }
func (s *OrderCancellations) Submit(ctx context.Context, key *domain.Key, snap OrderSnapshot) (*domain.OrderCancellation, error) {
	if key == nil || key.KeyID == "" || key.RevokedAt != nil || !s.Now().Before(key.ExpiresAt) ||
		!HasScope(key.Scopes, ScopeOrderCancelRequest) {
		return nil, ErrNotAuthorized
	}
	if snap.ID == 0 || len(snap.OrderNo) < 1 || len(snap.OrderNo) > 100 ||
		len(snap.Total) < 1 || len(snap.Total) > 40 ||
		len(snap.Currency) < 2 || len(snap.Currency) > 12 ||
		snap.Status != constants.OrderStatusPendingPayment || snap.UpdatedAt.IsZero() ||
		!utf8.ValidString(snap.OrderNo) {
		return nil, ErrInvalid
	}
	for _, v := range snap.OrderNo {
		if unicode.IsControl(v) {
			return nil, ErrInvalid
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	req := &domain.OrderCancellation{
		ID: id, KeyID: key.KeyID, OrderID: snap.ID, OrderNo: snap.OrderNo,
		Currency:       snap.Currency,
		ExpectedStatus: snap.Status, ExpectedTotal: snap.Total,
		ExpectedUpdatedAt: snap.UpdatedAt, Status: domain.ActionPending,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(OrderCancellationTTL),
	}
	audit := &domain.Audit{KeyID: key.KeyID, Action: "order_cancel_request", Route: "mcp/order_cancel", Result: "pending", CreatedAt: now}
	if err := s.repo.CreateOrderCancellation(ctx, req, audit, key.TokenHash); err != nil {
		return nil, err
	}
	return req, nil
}
func (s *OrderCancellations) Owned(ctx context.Context, key *domain.Key, id string) (*domain.OrderCancellation, error) {
	if key == nil || !actionIDPattern.MatchString(id) {
		return nil, ErrOrderCancellationUnavailable
	}
	req, err := s.repo.GetOrderCancellation(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil || req.KeyID != key.KeyID {
		return nil, ErrOrderCancellationUnavailable
	}
	return req, nil
}
func (s *OrderCancellations) GetForAdmin(ctx context.Context, id string) (*domain.OrderCancellation, error) {
	if !actionIDPattern.MatchString(id) {
		return nil, ErrOrderCancellationUnavailable
	}
	req, err := s.repo.GetOrderCancellation(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrOrderCancellationUnavailable
	}
	return req, nil
}
func (s *OrderCancellations) List(ctx context.Context, limit int) ([]domain.OrderCancellation, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return s.repo.ListOrderCancellations(ctx, limit)
}
func (s *OrderCancellations) Claim(ctx context.Context, id string, adminID uint) (*domain.OrderCancellation, error) {
	if adminID == 0 || !actionIDPattern.MatchString(id) {
		return nil, ErrInvalid
	}
	ok, err := s.repo.ClaimOrderCancellation(ctx, id, adminID, s.Now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrOrderCancellationUnavailable
	}
	return s.repo.GetOrderCancellation(ctx, id)
}
func (s *OrderCancellations) Reject(ctx context.Context, id string, adminID uint) error {
	if adminID == 0 || !actionIDPattern.MatchString(id) {
		return ErrInvalid
	}
	ok, err := s.repo.RejectOrderCancellation(ctx, id, adminID, s.Now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrOrderCancellationUnavailable
	}
	return nil
}
func (s *OrderCancellations) Complete(ctx context.Context, id, status, code string) error {
	if !actionIDPattern.MatchString(id) || (status != domain.ActionSucceeded && status != domain.ActionConflict && status != domain.ActionFailed) ||
		len(code) > 40 {
		return ErrInvalid
	}
	return s.repo.FinishOrderCancellation(ctx, id, status, code, s.Now())
}
