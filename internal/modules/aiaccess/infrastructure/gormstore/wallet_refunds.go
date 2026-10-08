package gormstore

import (
	"context"
	"errors"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"gorm.io/gorm"
	"time"
)

// The queue is separate from money movements. Claiming and finalizing are
// durably audited, one-shot transactions. Unexpected crashes leave "executing"
// and require human reconciliation, never an automatic second wallet credit.
func (s *Store) CreateWalletRefund(ctx context.Context, item *domain.WalletRefundRequest, audit *domain.Audit, hash string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var key domain.Key
		if err := tx.Where("key_id = ? AND token_hash = ? AND revoked_at IS NULL AND expires_at > ?",
			item.KeyID, hash, time.Now().UTC()).First(&key).Error; err != nil {
			return err
		}
		if !validScope(key.Scopes, "orders:wallet-refund:request") {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return tx.Create(audit).Error
	})
}
func (s *Store) GetWalletRefund(ctx context.Context, id string) (*domain.WalletRefundRequest, error) {
	var v domain.WalletRefundRequest
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
func (s *Store) ListWalletRefunds(ctx context.Context, limit int) ([]domain.WalletRefundRequest, error) {
	var items []domain.WalletRefundRequest
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}
func (s *Store) ClaimWalletRefund(ctx context.Context, id string, admin uint, now time.Time) (bool, error) {
	claimed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request domain.WalletRefundRequest
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
		if !validScope(key.Scopes, "orders:wallet-refund:request") {
			return nil
		}
		result := tx.Model(&domain.WalletRefundRequest{}).Where("id = ? AND status = ? AND expires_at > ?", id, domain.ActionPending, now).
			Updates(map[string]any{"status": domain.ActionExecuting, "reviewed_at": now, "reviewed_by": admin, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Create(&domain.Audit{KeyID: request.KeyID, ActorAdminID: admin,
			Action: "ai_wallet_refund_approve", Route: "ai/wallet_refund", Result: "claimed", CreatedAt: now}).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed && err == nil, err
}
func (s *Store) RejectWalletRefund(ctx context.Context, id string, admin uint, now time.Time) (bool, error) {
	ok := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var req domain.WalletRefundRequest
		err := tx.Where("id = ? AND status = ?", id, domain.ActionPending).First(&req).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		update := tx.Model(&domain.WalletRefundRequest{}).Where("id = ? AND status = ?", id, domain.ActionPending).
			Updates(map[string]any{"status": domain.ActionRejected, "reviewed_at": now, "reviewed_by": admin, "updated_at": now})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return nil
		}
		if err := tx.Create(&domain.Audit{KeyID: req.KeyID, ActorAdminID: admin,
			Action: "ai_wallet_refund_reject", Route: "ai/wallet_refund", Result: "rejected", CreatedAt: now}).Error; err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok && err == nil, err
}
func (s *Store) FinishWalletRefund(ctx context.Context, id, status, code string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request domain.WalletRefundRequest
		err := tx.Where("id = ? AND status = ?", id, domain.ActionExecuting).First(&request).Error
		if err != nil {
			return err
		}
		result := tx.Model(&domain.WalletRefundRequest{}).Where("id = ? AND status = ?", id, domain.ActionExecuting).
			Updates(map[string]any{"status": status, "failure_code": code, "completed_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(&domain.Audit{KeyID: request.KeyID, ActorAdminID: request.ReviewedBy,
			Action: "ai_wallet_refund_complete", Route: "ai/wallet_refund", Result: status, CreatedAt: now}).Error
	})
}
