package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dujiao-next/internal/modules/aiaccess/contract"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

var (
	ErrActionNotAvailable = errors.New("AI action unavailable, expired, denied or already processed")
	actionIDPattern       = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

const ActionReviewTTL = 24 * time.Hour

// Actions controls durable AI business change requests. It NEVER executes a
// business change; execution is performed only by the admin controller after a
// CAS claim commits and all merchant-state preconditions pass.
type Actions struct {
	repo contract.ActionRepository
	now  func() time.Time
}

func NewActions(repo contract.ActionRepository) *Actions { return &Actions{repo: repo, now: time.Now} }
func (s *Actions) Now() time.Time                        { return s.now().UTC() }

// ProductStatusSnapshot captures the source state presented to the human
// approver, so their consent is never silently repurposed for a newer change.
type ProductStatusSnapshot struct {
	ProductID     uint
	Title         string
	CurrentActive bool
	DesiredActive bool
	Price         string
	UpdatedAt     time.Time
}

func (s *Actions) SubmitStatus(ctx context.Context, key *domain.Key, input ProductStatusSnapshot) (*domain.ActionRequest, error) {
	if key == nil || key.KeyID == "" || key.RevokedAt != nil || !s.Now().Before(key.ExpiresAt) || !HasScope(key.Scopes, ScopePublishRequest) {
		return nil, ErrNotAuthorized
	}
	if input.ProductID == 0 || input.UpdatedAt.IsZero() || input.DesiredActive == input.CurrentActive || len(input.Price) < 1 || len(input.Price) > 40 {
		return nil, ErrInvalid
	}
	title := strings.TrimSpace(input.Title)
	if len(title) > 240 || !utf8.ValidString(title) {
		return nil, ErrInvalid
	}
	if title == "" {
		title = "(unnamed product)"
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return nil, ErrInvalid
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	item := &domain.ActionRequest{
		ID: id, KeyID: key.KeyID, Action: domain.ActionProductStatus,
		ProductID: input.ProductID, ProductTitle: title, ExpectedActive: input.CurrentActive,
		DesiredActive: input.DesiredActive, ExpectedPrice: input.Price,
		ExpectedUpdatedAt: input.UpdatedAt, Status: domain.ActionPending,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(ActionReviewTTL),
	}
	audit := &domain.Audit{KeyID: key.KeyID, Action: "action_request", Route: "ai/product_status", Result: "pending", CreatedAt: now}
	if err = s.repo.CreateAction(ctx, item, audit); err != nil {
		return nil, err
	}
	return item, nil
}
func (s *Actions) Owned(ctx context.Context, key *domain.Key, id string) (*domain.ActionRequest, error) {
	if key == nil || !actionIDPattern.MatchString(id) {
		return nil, ErrActionNotAvailable
	}
	item, err := s.repo.GetAction(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil || item.KeyID != key.KeyID {
		return nil, ErrActionNotAvailable
	}
	return item, nil
}
func (s *Actions) List(ctx context.Context, limit int) ([]domain.ActionRequest, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return s.repo.ListActions(ctx, limit)
}
func (s *Actions) Claim(ctx context.Context, id string, admin uint) (*domain.ActionRequest, error) {
	if admin == 0 || !actionIDPattern.MatchString(id) {
		return nil, ErrInvalid
	}
	ok, err := s.repo.ClaimAction(ctx, id, admin, s.Now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrActionNotAvailable
	}
	return s.repo.GetAction(ctx, id)
}
func (s *Actions) Reject(ctx context.Context, id string, admin uint) error {
	if admin == 0 || !actionIDPattern.MatchString(id) {
		return ErrInvalid
	}
	ok, err := s.repo.RejectAction(ctx, id, admin, s.Now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrActionNotAvailable
	}
	return nil
}
func (s *Actions) Complete(ctx context.Context, id, status, reason string) error {
	if !actionIDPattern.MatchString(id) || (status != domain.ActionSucceeded && status != domain.ActionConflict && status != domain.ActionFailed) {
		return ErrInvalid
	}
	if len(reason) > 40 {
		return ErrInvalid
	}
	return s.repo.FinishAction(ctx, id, status, reason, s.Now())
}
