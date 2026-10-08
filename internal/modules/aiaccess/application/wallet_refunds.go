package application

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dujiao-next/internal/modules/aiaccess/contract"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/shopspring/decimal"
)

var (
	ErrWalletRefundUnavailable = errors.New("AI wallet refund unavailable")
	RefundReasons              = map[string]bool{
		"customer_request": true, "duplicate_purchase": true,
		"undelivered": true, "other": true,
	}
)

const WalletRefundRequestTTL = 30 * time.Minute

type WalletRefundSnapshot struct {
	OrderID   uint
	OrderNo   string
	Currency  string
	Status    string
	Total     string
	Refunded  string
	UpdatedAt time.Time
}
type WalletRefunds struct {
	repo contract.WalletRefundRepository
	now  func() time.Time
}

func NewWalletRefunds(repo contract.WalletRefundRepository) *WalletRefunds {
	return &WalletRefunds{repo: repo, now: time.Now}
}
func (s *WalletRefunds) Now() time.Time { return s.now().UTC() }

func (s *WalletRefunds) Submit(ctx context.Context, key *domain.Key, snap WalletRefundSnapshot, amountRaw, reason string) (*domain.WalletRefundRequest, error) {
	if key == nil || key.KeyID == "" || key.RevokedAt != nil ||
		!s.Now().Before(key.ExpiresAt) || !HasScope(key.Scopes, ScopeWalletRefundRequest) {
		return nil, ErrNotAuthorized
	}
	if snap.OrderID == 0 || len(snap.OrderNo) < 1 || len(snap.OrderNo) > 100 ||
		len(snap.Status) < 2 || len(snap.Status) > 40 || snap.Currency != "CNY" ||
		len(snap.Total) < 1 || len(snap.Total) > 40 || len(snap.Refunded) < 1 || len(snap.Refunded) > 40 ||
		snap.UpdatedAt.IsZero() || !RefundReasons[reason] || !utf8.ValidString(snap.OrderNo) {
		return nil, ErrInvalid
	}
	for _, r := range snap.OrderNo {
		if unicode.IsControl(r) {
			return nil, ErrInvalid
		}
	}
	if len(amountRaw) < 1 || len(amountRaw) > 16 {
		return nil, ErrInvalid
	}
	amount, err := decimal.NewFromString(amountRaw)
	if err != nil || amount.Exponent() < -2 || amount.LessThan(decimal.RequireFromString("0.01")) ||
		amount.GreaterThan(decimal.RequireFromString("500.00")) {
		return nil, ErrInvalid
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	request := &domain.WalletRefundRequest{
		ID: id, KeyID: key.KeyID, OrderID: snap.OrderID, OrderNo: snap.OrderNo,
		Currency: snap.Currency, ExpectedStatus: snap.Status, ExpectedTotal: snap.Total,
		ExpectedRefunded: snap.Refunded, ExpectedUpdatedAt: snap.UpdatedAt,
		Amount: amount.StringFixed(2), Reason: reason,
		Status: domain.ActionPending, CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(WalletRefundRequestTTL),
	}
	audit := &domain.Audit{
		KeyID: key.KeyID, Action: "ai_wallet_refund_request",
		Route: "mcp/wallet_refund", Result: "pending", CreatedAt: now,
	}
	if err = s.repo.CreateWalletRefund(ctx, request, audit, key.TokenHash); err != nil {
		return nil, err
	}
	return request, nil
}
func (s *WalletRefunds) Owned(ctx context.Context, key *domain.Key, id string) (*domain.WalletRefundRequest, error) {
	if key == nil || !actionIDPattern.MatchString(id) {
		return nil, ErrWalletRefundUnavailable
	}
	request, err := s.repo.GetWalletRefund(ctx, id)
	if err != nil {
		return nil, err
	}
	if request == nil || request.KeyID != key.KeyID {
		return nil, ErrWalletRefundUnavailable
	}
	return request, nil
}
func (s *WalletRefunds) GetForAdmin(ctx context.Context, id string) (*domain.WalletRefundRequest, error) {
	if !actionIDPattern.MatchString(id) {
		return nil, ErrWalletRefundUnavailable
	}
	request, err := s.repo.GetWalletRefund(ctx, id)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, ErrWalletRefundUnavailable
	}
	return request, nil
}
func (s *WalletRefunds) List(ctx context.Context, limit int) ([]domain.WalletRefundRequest, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return s.repo.ListWalletRefunds(ctx, limit)
}
func (s *WalletRefunds) Claim(ctx context.Context, id string, admin uint) (*domain.WalletRefundRequest, error) {
	if admin == 0 || !actionIDPattern.MatchString(id) {
		return nil, ErrInvalid
	}
	ok, err := s.repo.ClaimWalletRefund(ctx, id, admin, s.Now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrWalletRefundUnavailable
	}
	return s.repo.GetWalletRefund(ctx, id)
}
func (s *WalletRefunds) Reject(ctx context.Context, id string, admin uint) error {
	if admin == 0 || !actionIDPattern.MatchString(id) {
		return ErrInvalid
	}
	ok, err := s.repo.RejectWalletRefund(ctx, id, admin, s.Now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrWalletRefundUnavailable
	}
	return nil
}
func (s *WalletRefunds) Complete(ctx context.Context, id, status, code string) error {
	if !actionIDPattern.MatchString(id) ||
		(status != domain.ActionSucceeded && status != domain.ActionFailed && status != domain.ActionConflict) ||
		len(code) > 40 {
		return ErrInvalid
	}
	return s.repo.FinishWalletRefund(ctx, id, status, code, s.Now())
}
