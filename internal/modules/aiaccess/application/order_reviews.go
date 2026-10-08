package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/dujiao-next/internal/modules/aiaccess/contract"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

var (
	ErrOrderReviewUnavailable = errors.New("AI order review unavailable")
	orderReviewID             = regexp.MustCompile(`^[a-f0-9]{32}$`)
	allowedReviewReasons      = map[string]bool{
		"refund_review": true, "delivery_delay": true,
		"payment_exception": true, "cancellation_request": true, "other_exception": true,
	}
)

const OrderReviewTTL = 48 * time.Hour

type OrderSnapshot struct {
	ID        uint
	OrderNo   string
	Status    string
	Currency  string
	Total     string
	UpdatedAt time.Time
}
type OrderReviews struct {
	repo contract.OrderReviewRepository
	now  func() time.Time
}

func NewOrderReviews(repo contract.OrderReviewRepository) *OrderReviews {
	return &OrderReviews{repo: repo, now: time.Now}
}
func (s *OrderReviews) Now() time.Time { return s.now().UTC() }
func (s *OrderReviews) Submit(ctx context.Context, key *domain.Key, order OrderSnapshot, reason string) (*domain.OrderReview, error) {
	if key == nil || key.KeyID == "" || key.RevokedAt != nil ||
		!s.Now().Before(key.ExpiresAt) || !HasScope(key.Scopes, ScopeOrderReviewRequest) {
		return nil, ErrNotAuthorized
	}
	if order.ID == 0 || len(order.OrderNo) > 100 || order.OrderNo == "" ||
		len(order.Status) < 1 || len(order.Status) > 40 || len(order.Currency) > 12 ||
		order.UpdatedAt.IsZero() || len(order.Total) < 1 || len(order.Total) > 40 || !allowedReviewReasons[reason] {
		return nil, ErrInvalid
	}
	for _, x := range order.OrderNo + order.Status + order.Currency {
		if unicode.IsControl(x) {
			return nil, ErrInvalid
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	review := &domain.OrderReview{
		ID: id, KeyID: key.KeyID, OrderID: order.ID, OrderNo: strings.TrimSpace(order.OrderNo),
		Reason: reason, ExpectedStatus: order.Status, ExpectedTotal: order.Total,
		Currency: order.Currency, ExpectedUpdatedAt: order.UpdatedAt,
		Status: domain.OrderReviewPending, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(OrderReviewTTL),
	}
	audit := &domain.Audit{KeyID: key.KeyID, Action: "order_review_request", Route: "mcp/order_review", Result: "pending", CreatedAt: now}
	if err = s.repo.CreateOrderReview(ctx, review, audit); err != nil {
		return nil, err
	}
	return review, nil
}
func (s *OrderReviews) Owned(ctx context.Context, key *domain.Key, id string) (*domain.OrderReview, error) {
	if key == nil || !orderReviewID.MatchString(id) {
		return nil, ErrOrderReviewUnavailable
	}
	item, err := s.repo.GetOrderReview(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil || item.KeyID != key.KeyID {
		return nil, ErrOrderReviewUnavailable
	}
	return item, nil
}
func (s *OrderReviews) GetForAdmin(ctx context.Context, id string) (*domain.OrderReview, error) {
	if !orderReviewID.MatchString(id) {
		return nil, ErrOrderReviewUnavailable
	}
	item, err := s.repo.GetOrderReview(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrOrderReviewUnavailable
	}
	return item, nil
}
func (s *OrderReviews) List(ctx context.Context, limit int) ([]domain.OrderReview, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return s.repo.ListOrderReviews(ctx, limit)
}
func (s *OrderReviews) Process(ctx context.Context, id string, admin uint, action string) (string, error) {
	if admin == 0 || !orderReviewID.MatchString(id) {
		return "", ErrInvalid
	}
	switch action {
	case "accept", "reject", "resolve", "conflict":
	default:
		return "", ErrInvalid
	}
	status, err := s.repo.ProcessOrderReview(ctx, id, admin, action, s.Now())
	if err != nil {
		return "", err
	}
	if status == "" {
		return "", ErrOrderReviewUnavailable
	}
	return status, nil
}
