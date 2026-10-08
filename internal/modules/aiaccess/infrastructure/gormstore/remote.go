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
		updates := map[string]any{"enabled": config.Enabled, "public_origin": config.PublicOrigin, "updated_at": time.Now().UTC()}
		// Switching off HTTPS MCP also disarms its independent write mode.
		if !config.Enabled {
			updates["website_write_enabled"] = false
		}
		return tx.Model(&domain.RemoteConfig{}).Where("id = ?", 1).Updates(updates).Error
	})
}

// SaveControl is an independent system-admin operation. The AI protocol has
// no tool or OAuth scope allowing it to call this method.
func (s *Store) SaveControl(ctx context.Context, master, write bool, audit *domain.Audit) error {
	if audit == nil || audit.ActorAdminID == 0 {
		return errors.New("system administrator required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var settings domain.RemoteConfig
		err := tx.First(&settings, 1).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			settings = domain.RemoteConfig{ID: 1}
			if err = tx.Create(&settings).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if write && (!master || !settings.Enabled || settings.PublicOrigin == "") {
			return errors.New("remote HTTPS MCP must be configured and master AI enabled before website writing")
		}
		if !master {
			write = false
		}
		audit.KeyID = "ai-system"
		audit.Route = "admin/ai-control"
		audit.Result = "master_off"
		if master {
			audit.Result = "master_on"
		}
		if write {
			audit.Result = "website_write_on"
		}
		audit.CreatedAt = time.Now().UTC()
		if err := tx.Model(&domain.RemoteConfig{}).Where("id = 1").
			Updates(map[string]any{"master_enabled": master, "website_write_enabled": write, "updated_at": audit.CreatedAt}).Error; err != nil {
			return err
		}
		return tx.Create(audit).Error
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
