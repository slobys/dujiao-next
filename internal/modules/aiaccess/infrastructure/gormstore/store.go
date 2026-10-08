package gormstore

import (
	"context"
	"errors"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"gorm.io/gorm"
	"time"
)

type Store struct{ db *gorm.DB }

func New(db *gorm.DB) *Store { return &Store{db: db} }
func (s *Store) CreateWithAudit(ctx context.Context, key *domain.Key, audit *domain.Audit) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(key).Error; err != nil {
			return err
		}
		return tx.Create(audit).Error
	})
}
func (s *Store) GetByID(ctx context.Context, id uint) (*domain.Key, error) {
	var item domain.Key
	err := s.db.WithContext(ctx).First(&item, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func (s *Store) FindKey(ctx context.Context, keyID string) (*domain.Key, error) {
	var item domain.Key
	err := s.db.WithContext(ctx).Where("key_id = ?", keyID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func (s *Store) List(ctx context.Context) ([]domain.Key, error) {
	var items []domain.Key
	err := s.db.WithContext(ctx).Order("id desc").Limit(100).Find(&items).Error
	return items, err
}
func (s *Store) RotateWithAudit(ctx context.Context, id uint, hash string, now time.Time, audit *domain.Audit) (bool, error) {
	updated := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Key{}).
			Where("id = ? AND revoked_at IS NULL AND expires_at > ?", id, now).
			Updates(map[string]interface{}{"token_hash": hash, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		// Manual rotation of an OAuth key must also invalidate any issued
		// refresh token, otherwise old remote sessions could mint a fresh key.
		if err := tx.Where("key_id = ?", audit.KeyID).Delete(&domain.OAuthRefresh{}).Error; err != nil {
			return err
		}
		// Revoking a compromised access secret must also invalidate every
		// still-pending AI proposal authored by that key. Old proposals
		// cannot later be approved under a newly rotated credential.
		if err := tx.Model(&domain.ActionRequest{}).
			Where("key_id = ? AND status = ?", audit.KeyID, domain.ActionPending).
			Updates(map[string]interface{}{"status": domain.ActionRejected, "failure_code": "key_rotated", "updated_at": now}).Error; err != nil {
			return err
		}
		// A rotated AI identity cannot leave older after-sales requests
		// silently awaiting acceptance under its newly issued credential.
		if err := tx.Model(&domain.OrderReview{}).
			Where("key_id = ? AND status = ?", audit.KeyID, domain.OrderReviewPending).
			Updates(map[string]any{"status": domain.OrderReviewRejected, "updated_at": now}).Error; err != nil {
			return err
		}
		// A rotated credential invalidates requests authored by its OLD
		// access token, even if the same key ID receives a new secret.
		if err := tx.Model(&domain.OrderCancellation{}).
			Where("key_id = ? AND status = ?", audit.KeyID, domain.ActionPending).
			Updates(map[string]interface{}{"status": domain.ActionRejected, "failure_code": "key_rotated", "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Create(audit).Error; err != nil {
			return err
		}
		updated = true
		return nil
	})
	return updated && err == nil, err
}
func (s *Store) RevokeWithAudit(ctx context.Context, id uint, now time.Time, audit *domain.Audit) (bool, error) {
	updated := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Key{}).
			Where("id = ? AND revoked_at IS NULL", id).
			Updates(map[string]interface{}{"revoked_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Create(audit).Error; err != nil {
			return err
		}
		updated = true
		return nil
	})
	return updated && err == nil, err
}
func (s *Store) Touch(ctx context.Context, id uint, now time.Time) error {
	result := s.db.WithContext(ctx).Model(&domain.Key{}).
		Where("id = ? AND revoked_at IS NULL AND expires_at > ?", id, now).
		UpdateColumn("last_used_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
func (s *Store) Audit(ctx context.Context, item *domain.Audit) error {
	return s.db.WithContext(ctx).Create(item).Error
}
func (s *Store) Audits(ctx context.Context, limit int) ([]domain.Audit, error) {
	var items []domain.Audit
	err := s.db.WithContext(ctx).Order("id desc").Limit(limit).Find(&items).Error
	return items, err
}
