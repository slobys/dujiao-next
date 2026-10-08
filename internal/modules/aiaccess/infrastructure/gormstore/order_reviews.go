package gormstore

import (
	"context"
	"errors"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"gorm.io/gorm"
)

func (s *Store) CreateOrderReview(ctx context.Context, item *domain.OrderReview, audit *domain.Audit) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Atomic guard against revocation between MCP authentication and
		// database submission: an invalid or rotated AI identity cannot
		// leave a new pending after-sales ticket behind.
		var key domain.Key
		if err := tx.Where("key_id = ? AND revoked_at IS NULL AND expires_at > ?", item.KeyID, time.Now().UTC()).First(&key).Error; err != nil {
			return err
		}
		if !validScope(key.Scopes, "orders:review:request") {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return tx.Create(audit).Error
	})
}
func (s *Store) GetOrderReview(ctx context.Context, id string) (*domain.OrderReview, error) {
	var item domain.OrderReview
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func (s *Store) ListOrderReviews(ctx context.Context, limit int) ([]domain.OrderReview, error) {
	var items []domain.OrderReview
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}

// ProcessOrderReview is a once-only state machine with transactional audit.
// "accepted" only opens a human follow-up ticket, NEVER mutates any order,
// inventory, payment, delivery or refund table.
func (s *Store) ProcessOrderReview(ctx context.Context, id string, admin uint, action string, now time.Time) (string, error) {
	next := ""
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item domain.OrderReview
		err := tx.Where("id = ?", id).First(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		updates := map[string]any{"reviewed_by": admin, "reviewed_at": now, "updated_at": now}
		expected := domain.OrderReviewPending
		switch action {
		case "accept":
			if item.Status != domain.OrderReviewPending || !now.Before(item.ExpiresAt) {
				return nil
			}
			var key domain.Key
			err := tx.Where("key_id = ? AND revoked_at IS NULL AND expires_at > ?", item.KeyID, now).First(&key).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if !validScope(key.Scopes, "orders:review:request") {
				return nil
			}
			next = domain.OrderReviewAccepted
		case "reject":
			if item.Status != domain.OrderReviewPending {
				return nil
			}
			next = domain.OrderReviewRejected
		case "conflict":
			if item.Status != domain.OrderReviewPending {
				return nil
			}
			next = domain.OrderReviewConflict
		case "resolve":
			if item.Status != domain.OrderReviewAccepted {
				return nil
			}
			expected = domain.OrderReviewAccepted
			next = domain.OrderReviewResolved
			updates["resolved_at"] = now
		default:
			return errors.New("invalid AI order review transition")
		}
		updates["status"] = next
		result := tx.Model(&domain.OrderReview{}).Where("id = ? AND status = ?", id, expected).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			next = ""
			return nil
		}
		audit := domain.Audit{
			KeyID: item.KeyID, ActorAdminID: admin, Action: "order_review_" + action,
			Route: "ai/order_review", Result: next, CreatedAt: now,
		}
		return tx.Create(&audit).Error
	})
	if err != nil {
		return "", err
	}
	return next, nil
}

func (s *Store) CreateOrderCancellation(ctx context.Context, item *domain.OrderCancellation, audit *domain.Audit, expectedTokenHash string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var key domain.Key
		err := tx.Where("key_id = ? AND token_hash = ? AND revoked_at IS NULL AND expires_at > ?", item.KeyID, expectedTokenHash, time.Now().UTC()).First(&key).Error
		if err != nil {
			return err
		}
		if !validScope(key.Scopes, "orders:cancel:request") {
			return gorm.ErrRecordNotFound
		}
		if err = tx.Create(item).Error; err != nil {
			return err
		}
		return tx.Create(audit).Error
	})
}
func (s *Store) GetOrderCancellation(ctx context.Context, id string) (*domain.OrderCancellation, error) {
	var item domain.OrderCancellation
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func (s *Store) ListOrderCancellations(ctx context.Context, limit int) ([]domain.OrderCancellation, error) {
	var records []domain.OrderCancellation
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&records).Error
	return records, err
}
func (s *Store) ClaimOrderCancellation(ctx context.Context, id string, admin uint, now time.Time) (bool, error) {
	claimed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request domain.OrderCancellation
		err := tx.Where("id = ? AND status = ? AND expires_at > ?", id, domain.ActionPending, now).First(&request).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var key domain.Key
		err = tx.Where("key_id = ? AND revoked_at IS NULL AND expires_at > ?", request.KeyID, now).First(&key).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !validScope(key.Scopes, "orders:cancel:request") {
			return nil
		}
		r := tx.Model(&domain.OrderCancellation{}).
			Where("id = ? AND status = ? AND expires_at > ?", id, domain.ActionPending, now).
			Updates(map[string]interface{}{"status": domain.ActionExecuting, "reviewed_by": admin, "reviewed_at": now, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return nil
		}
		audit := &domain.Audit{
			KeyID: request.KeyID, ActorAdminID: admin, Action: "order_cancel_approve",
			Route: "ai/order_cancellation", Result: "claimed", CreatedAt: now,
		}
		if err = tx.Create(audit).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed && err == nil, err
}
func (s *Store) RejectOrderCancellation(ctx context.Context, id string, admin uint, now time.Time) (bool, error) {
	rejected := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item domain.OrderCancellation
		err := tx.Where("id = ? AND status = ?", id, domain.ActionPending).First(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		r := tx.Model(&domain.OrderCancellation{}).
			Where("id = ? AND status = ?", id, domain.ActionPending).
			Updates(map[string]interface{}{"status": domain.ActionRejected, "reviewed_by": admin, "reviewed_at": now, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return nil
		}
		if err = tx.Create(&domain.Audit{
			KeyID: item.KeyID, ActorAdminID: admin, Action: "order_cancel_reject",
			Route: "ai/order_cancellation", Result: "rejected", CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		rejected = true
		return nil
	})
	return rejected && err == nil, err
}
func (s *Store) FinishOrderCancellation(ctx context.Context, id, status, reason string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item domain.OrderCancellation
		if err := tx.Where("id = ? AND status = ?", id, domain.ActionExecuting).First(&item).Error; err != nil {
			return err
		}
		r := tx.Model(&domain.OrderCancellation{}).
			Where("id = ? AND status = ?", id, domain.ActionExecuting).
			Updates(map[string]interface{}{"status": status, "failure_code": reason, "completed_at": now, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(&domain.Audit{
			KeyID: item.KeyID, ActorAdminID: item.ReviewedBy, Action: "order_cancel_complete",
			Route: "ai/order_cancellation", Result: status, CreatedAt: now,
		}).Error
	})
}
