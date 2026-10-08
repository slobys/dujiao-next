package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"gorm.io/gorm"
)

func aiActionFixture(t *testing.T) (*gorm.DB, *app.Service, *app.Actions) {
	t.Helper()
	db, keys := setup(t)
	if err := db.AutoMigrate(&domain.ActionRequest{}); err != nil {
		t.Fatal(err)
	}
	return db, keys, app.NewActions(gormstore.New(db))
}
func actionSnapshot(desired bool) app.ProductStatusSnapshot {
	return app.ProductStatusSnapshot{
		ProductID: 101, Title: "演示商品", CurrentActive: !desired, DesiredActive: desired,
		Price: "29.90", UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}
func TestActionReviewIsOneTimeAndAudited(t *testing.T) {
	db, keys, actions := aiActionFixture(t)
	ctx := context.Background()
	read, _, err := keys.Create(ctx, "read", []string{app.ScopeCatalog}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = actions.SubmitStatus(ctx, read, actionSnapshot(true)); !errors.Is(err, app.ErrNotAuthorized) {
		t.Fatal("read-only key submitted write request")
	}
	key, _, err := keys.Create(ctx, "publish requester", []string{app.ScopePublishRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = actions.SubmitStatus(ctx, key, actionSnapshot(false)); err != nil {
		t.Fatal(err)
	}
	req, err := actions.SubmitStatus(ctx, key, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Owned(ctx, read, req.ID); err == nil {
		t.Fatal("other key can see request")
	}
	if owned, err := actions.Owned(ctx, key, req.ID); err != nil || owned.Status != domain.ActionPending {
		t.Fatalf("cannot read own request: %v", err)
	}
	var count int64
	if err = db.Model(&domain.ActionRequest{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("request persistence %d %v", count, err)
	}
	var successes int
	var lock sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := actions.Claim(ctx, req.ID, 5)
			if err == nil {
				lock.Lock()
				successes++
				lock.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("double approval! successful claims=%d", successes)
	}
	if err = actions.Reject(ctx, req.ID, 5); err == nil {
		t.Fatal("claimed request was rejected twice")
	}
	if err = actions.Complete(ctx, req.ID, domain.ActionSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if err = actions.Complete(ctx, req.ID, domain.ActionSucceeded, ""); err == nil {
		t.Fatal("completion replay allowed")
	}
	stored, err := actions.Owned(ctx, key, req.ID)
	if err != nil || stored.Status != domain.ActionSucceeded {
		t.Fatalf("unexpected final state: %+v %v", stored, err)
	}
	records, err := keys.Audits(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, v := range records {
		seen[v.Action]++
	}
	if seen["action_request"] != 2 || seen["action_approve"] != 1 || seen["action_complete"] != 1 {
		t.Fatalf("missing durable audit: %+v", seen)
	}
}
func TestExpiredAndRevokedRequestsCannotBeApproved(t *testing.T) {
	db, keys, actions := aiActionFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "OpenClaw", []string{app.ScopePublishRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := actions.SubmitStatus(ctx, key, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&domain.ActionRequest{}).Where("id=?", req.ID).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Claim(ctx, req.ID, 2); !errors.Is(err, app.ErrActionNotAvailable) {
		t.Fatal("expired action approved")
	}
	other, err := actions.SubmitStatus(ctx, key, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Revoke(ctx, key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Claim(ctx, other.ID, 2); !errors.Is(err, app.ErrActionNotAvailable) {
		t.Fatal("revoked key's pending request approved")
	}
	if err = actions.Reject(ctx, other.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = actions.Reject(ctx, other.ID, 2); err == nil {
		t.Fatal("duplicate rejection")
	}
}
func TestRotatedAIKeyCancelsUnapprovedActions(t *testing.T) {
	_, keys, actions := aiActionFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "OpenClaw", []string{app.ScopePublishRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := actions.SubmitStatus(ctx, key, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keys.Rotate(ctx, key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Claim(ctx, req.ID, 2); !errors.Is(err, app.ErrActionNotAvailable) {
		t.Fatal("rotated key left old pending action executable")
	}
	got, err := actions.Owned(ctx, key, req.ID)
	if err != nil || got.Status != domain.ActionRejected || got.FailureCode != "key_rotated" {
		t.Fatalf("rotation did not invalidate pending proposal: %+v %v", got, err)
	}
}

func TestPendingRequestAuditFailureRollsBack(t *testing.T) {
	db, keys, actions := aiActionFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "Bot", []string{app.ScopePublishRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Callback().Create().Before("gorm:create").Register("fail_action_audit", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "ai_access_audit_logs" {
			tx.AddError(fmt.Errorf("database audit unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("fail_action_audit")
	if _, err = actions.SubmitStatus(ctx, key, actionSnapshot(true)); err == nil {
		t.Fatal("request created without audit")
	}
	var count int64
	if err = db.Model(&domain.ActionRequest{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan request %d %v", count, err)
	}
}
