package integrationtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This suite only runs against an explicitly designated, disposable test DB.
// Never point TEST_POSTGRES_DSN at a live shop database.
func postgresOAuthFixture(t *testing.T) (*gorm.DB, *gormstore.Store, domain.Key, domain.OAuthRefresh) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" || os.Getenv("AIACCESS_TEST_POSTGRES_ISOLATED") != "1" {
		t.Skip("PostgreSQL OAuth lock tests need TEST_POSTGRES_DSN and AIACCESS_TEST_POSTGRES_ISOLATED=1")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&domain.Key{}, &domain.OAuthRefresh{}, &domain.Audit{},
		&domain.ActionRequest{}, &domain.OrderReview{},
		&domain.OrderCancellation{}, &domain.WalletRefundRequest{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("%016x", now.UnixNano())
	key := domain.Key{
		Name: "pg-oauth-lock", KeyID: id, TokenHash: strings.Repeat("a", 64),
		OAuthClientID: "pg-client", Audience: "https://pg.example/mcp",
		Scopes: "catalog:read", CreatedBy: 1, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	}
	refresh := domain.OAuthRefresh{
		KeyID: id, ClientID: key.OAuthClientID, Audience: key.Audience,
		TokenHash: strings.Repeat("b", 64), ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&refresh).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("key_id = ?", id).Delete(&domain.Audit{})
		db.Where("key_id = ?", id).Delete(&domain.OAuthRefresh{})
		db.Delete(&domain.Key{}, key.ID)
	})
	return db, gormstore.New(db), key, refresh
}

type refreshResult struct {
	ok  bool
	err error
}

// The administrator locks the key before deleting refresh tokens (as the
// production RotateWithAudit does). Refresh must not hold the refresh row
// while waiting for that key, or PostgreSQL detects a lock-order deadlock.
func TestPostgresOAuthRefreshVsManualRotationLockOrder(t *testing.T) {
	db, repo, key, refresh := postgresOAuthFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	adminTx := db.WithContext(ctx).Begin()
	if adminTx.Error != nil {
		t.Fatal(adminTx.Error)
	}
	defer adminTx.Rollback()
	rotatedHash := strings.Repeat("c", 64)
	if err := adminTx.Model(&domain.Key{}).Where("id = ?", key.ID).
		Update("token_hash", rotatedHash).Error; err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan refreshResult, 1)
	go func() {
		close(started)
		ok, err := repo.RefreshOAuth(ctx, key.KeyID, key.OAuthClientID, refresh.TokenHash,
			key.Audience, time.Now().UTC(), strings.Repeat("d", 64), strings.Repeat("e", 64),
			time.Now().UTC().Add(time.Hour),
			&domain.Audit{KeyID: key.KeyID, Action: "oauth_refresh", Result: "ok", CreatedAt: time.Now().UTC()})
		done <- refreshResult{ok: ok, err: err}
	}()
	<-started
	// Give the concurrent query a chance to run before the administrator
	// continues to the refresh table. This reproduces the conflicting locks.
	time.Sleep(120 * time.Millisecond)
	if err := adminTx.Where("key_id = ?", key.KeyID).Delete(&domain.OAuthRefresh{}).Error; err != nil {
		t.Fatalf("manual rotation blocked behind OAuth refresh: %v", err)
	}
	if err := adminTx.Commit().Error; err != nil {
		t.Fatalf("manual rotation commit: %v", err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.ok {
			t.Fatalf("refresh after manual rotation = %+v, want invalid_grant", result)
		}
	case <-ctx.Done():
		t.Fatal("refresh did not finish after key was rotated")
	}
	var got domain.Key
	if err := db.First(&got, key.ID).Error; err != nil || got.TokenHash != rotatedHash {
		t.Fatalf("manual key secret changed by expired refresh: %v %v", got.TokenHash, err)
	}
	var auditCount int64
	if err := db.Model(&domain.Audit{}).Where("key_id = ? AND action = ?", key.KeyID, "oauth_refresh").
		Count(&auditCount).Error; err != nil || auditCount != 0 {
		t.Fatalf("invalid refresh wrote success audit: count=%d err=%v", auditCount, err)
	}
}

func TestPostgresOAuthConcurrentRefreshSingleUse(t *testing.T) {
	db, repo, key, refresh := postgresOAuthFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results := make(chan refreshResult, 2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			accessHash := strings.Repeat(string(rune('c'+i)), 64)
			refreshHash := strings.Repeat(string(rune('e'+i)), 64)
			ok, err := repo.RefreshOAuth(ctx, key.KeyID, key.OAuthClientID, refresh.TokenHash,
				key.Audience, time.Now().UTC(), accessHash, refreshHash, time.Now().UTC().Add(time.Hour),
				&domain.Audit{KeyID: key.KeyID, Action: "oauth_refresh", Result: "ok", CreatedAt: time.Now().UTC()})
			results <- refreshResult{ok: ok, err: err}
		}()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("concurrent refresh error: %v", result.err)
			}
			if result.ok {
				successes++
			}
		case <-ctx.Done():
			t.Fatal("concurrent refresh deadlocked")
		}
	}
	if successes != 1 {
		t.Fatalf("one-time OAuth refresh accepted %d requests; want exactly 1", successes)
	}
	var auditCount int64
	if err := db.Model(&domain.Audit{}).Where("key_id = ? AND action = ?", key.KeyID, "oauth_refresh").
		Count(&auditCount).Error; err != nil || auditCount != 1 {
		t.Fatalf("concurrent refresh audit count=%d, err=%v", auditCount, err)
	}
}
