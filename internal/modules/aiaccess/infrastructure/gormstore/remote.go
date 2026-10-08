package gormstore

import (
	"context"
	"errors"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"gorm.io/gorm"
)

func (s *Store) GetRemote(ctx context.Context) (*domain.RemoteConfig, error) {
	var v domain.RemoteConfig
	err := s.db.WithContext(ctx).First(&v, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.RemoteConfig{ID: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
func (s *Store) SaveRemote(ctx context.Context, config *domain.RemoteConfig) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var prev domain.RemoteConfig
		err := tx.First(&prev, 1).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			config.ID = 1
			return tx.Create(config).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&domain.RemoteConfig{}).Where("id = ?", 1).Updates(map[string]any{
			"enabled": config.Enabled, "public_origin": config.PublicOrigin, "updated_at": time.Now().UTC(),
		}).Error
	})
}
func (s *Store) RegisterClient(ctx context.Context, c *domain.OAuthClient) error {
	return s.db.WithContext(ctx).Create(c).Error
}
func (s *Store) FindClient(ctx context.Context, id string) (*domain.OAuthClient, error) {
	var client domain.OAuthClient
	err := s.db.WithContext(ctx).Where("client_id = ?", id).First(&client).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &client, nil
}
func (s *Store) CreateAuthorization(ctx context.Context, pending *domain.AuthorizationRequest) error {
	return s.db.WithContext(ctx).Create(pending).Error
}
func (s *Store) FindAuthorization(ctx context.Context, id string) (*domain.AuthorizationRequest, error) {
	var pending domain.AuthorizationRequest
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pending, nil
}
func (s *Store) ApproveAuthorization(ctx context.Context, id, codeHash, scopes string, admin uint, now time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Model(&domain.AuthorizationRequest{}).
		Where("id = ? AND code_hash = '' AND redeemed_at IS NULL AND expires_at > ?", id, now).
		Updates(map[string]any{"code_hash": codeHash, "approved_at": now, "approved_by": admin, "scopes": scopes})
	return result.RowsAffected == 1, result.Error
}
func (s *Store) DenyAuthorization(ctx context.Context, id string, now time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Model(&domain.AuthorizationRequest{}).
		Where("id = ? AND code_hash = '' AND approved_at IS NULL AND redeemed_at IS NULL AND expires_at > ?", id, now).
		Update("redeemed_at", now)
	return result.RowsAffected == 1, result.Error
}

func (s *Store) RedeemAuthorization(ctx context.Context, id, codeHash string, now time.Time, key *domain.Key, refresh *domain.OAuthRefresh, audit *domain.Audit) (bool, error) {
	redeemed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&domain.AuthorizationRequest{}).
			Where("id = ? AND code_hash = ? AND approved_at IS NOT NULL AND redeemed_at IS NULL AND expires_at > ?", id, codeHash, now).
			Update("redeemed_at", now)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return nil
		}
		if err := tx.Create(key).Error; err != nil {
			return err
		}
		if err := tx.Create(refresh).Error; err != nil {
			return err
		}
		if err := tx.Create(audit).Error; err != nil {
			return err
		}
		redeemed = true
		return nil
	})
	return redeemed && err == nil, err
}
func (s *Store) RefreshOAuth(ctx context.Context, keyID, clientID, refreshHash, audience string, now time.Time, accessHash, newRefreshHash string, accessExpiry time.Time, audit *domain.Audit) (bool, error) {
	rotated := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&domain.OAuthRefresh{}).
			Where("key_id = ? AND client_id = ? AND audience = ? AND token_hash = ? AND expires_at > ?", keyID, clientID, audience, refreshHash, now).
			Updates(map[string]any{"token_hash": newRefreshHash, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return nil
		}
		r = tx.Model(&domain.Key{}).
			Where("key_id = ? AND oauth_client_id = ? AND audience = ? AND revoked_at IS NULL", keyID, clientID, audience).
			Updates(map[string]any{"token_hash": accessHash, "expires_at": accessExpiry, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Create(audit).Error; err != nil {
			return err
		}
		rotated = true
		return nil
	})
	return rotated && err == nil, err
}
