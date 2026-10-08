package gormstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"gorm.io/gorm"
)

func (s *Store) CreateAction(ctx context.Context, action *domain.ActionRequest, audit *domain.Audit) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(action).Error; err != nil {
			return err
		}
		return tx.Create(audit).Error
	})
}
func (s *Store) GetAction(ctx context.Context, id string) (*domain.ActionRequest, error) {
	var item domain.ActionRequest
	err := s.db.WithContext(ctx).Where("id=?", id).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func (s *Store) ListActions(ctx context.Context, limit int) ([]domain.ActionRequest, error) {
	var items []domain.ActionRequest
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}
func validScope(raw, scope string) bool {
	for _, item := range strings.Split(raw, ",") {
		if item == scope {
			return true
		}
	}
	return false
}
func (s *Store) ClaimAction(ctx context.Context, id string, admin uint, now time.Time) (bool, error) {
	ok := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var action domain.ActionRequest
		err := tx.Where("id=? AND status=? AND expires_at > ?", id, domain.ActionPending, now).First(&action).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var key domain.Key
		err = tx.Where("key_id=? AND revoked_at IS NULL AND expires_at > ?", action.KeyID, now).First(&key).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !validScope(key.Scopes, "catalog:publish:request") {
			return nil
		}
		result := tx.Model(&domain.ActionRequest{}).
			Where("id=? AND status=? AND expires_at > ?", id, domain.ActionPending, now).
			Updates(map[string]any{"status": domain.ActionExecuting, "reviewed_by": admin, "reviewed_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		audit := domain.Audit{KeyID: action.KeyID, ActorAdminID: admin, Action: "action_approve", Route: "ai/product_status", Result: "claimed", CreatedAt: now}
		if err = tx.Create(&audit).Error; err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok && err == nil, err
}
func (s *Store) RejectAction(ctx context.Context, id string, admin uint, now time.Time) (bool, error) {
	ok := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var action domain.ActionRequest
		err := tx.Where("id=? AND status=?", id, domain.ActionPending).First(&action).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		result := tx.Model(&domain.ActionRequest{}).Where("id=? AND status=?", id, domain.ActionPending).
			Updates(map[string]any{"status": domain.ActionRejected, "reviewed_by": admin, "reviewed_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		audit := domain.Audit{KeyID: action.KeyID, ActorAdminID: admin, Action: "action_reject", Route: "ai/product_status", Result: "rejected", CreatedAt: now}
		if err = tx.Create(&audit).Error; err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok && err == nil, err
}
func (s *Store) FinishAction(ctx context.Context, id, status, reason string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var action domain.ActionRequest
		if err := tx.Where("id=? AND status=?", id, domain.ActionExecuting).First(&action).Error; err != nil {
			return err
		}
		result := tx.Model(&domain.ActionRequest{}).Where("id=? AND status=?", id, domain.ActionExecuting).
			Updates(map[string]any{"status": status, "failure_code": reason, "completed_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(&domain.Audit{
			KeyID: action.KeyID, ActorAdminID: action.ReviewedBy,
			Action: "action_complete", Route: "ai/product_status", Result: status, CreatedAt: now,
		}).Error
	})
}
