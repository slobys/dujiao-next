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
