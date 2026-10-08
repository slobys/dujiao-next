package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"gorm.io/gorm"
)

func cancelRequestsFixture(t *testing.T) (*gorm.DB, *app.Service, *app.OrderCancellations) {
	t.Helper()
	db, keys := setup(t)
	return db, keys, app.NewOrderCancellations(gormstore.New(db))
}
func pendingOrderCancellationSnap() app.OrderSnapshot {
	return app.OrderSnapshot{
		ID: 702, OrderNo: "TEST-NO-PAYMENT-702", Status: constants.OrderStatusPendingPayment,
		Currency: "CNY", Total: "32.90", UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
}
func TestAICancellationRequestsRequireScopeAndAreOneTime(t *testing.T) {
	_, keys, actions := cancelRequestsFixture(t)
	ctx := context.Background()
	viewer, _, err := keys.Create(ctx, "read", []string{app.ScopeOrdersRead}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	snap := pendingOrderCancellationSnap()
	if _, err = actions.Submit(ctx, viewer, snap); !errors.Is(err, app.ErrNotAuthorized) {
		t.Fatalf("read-only key submitted cancellation: %v", err)
	}
	key, _, err := keys.Create(ctx, "cancel-requester", []string{app.ScopeOrderCancelRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	illegal := snap
	illegal.Status = constants.OrderStatusPaid
	if _, err = actions.Submit(ctx, key, illegal); !errors.Is(err, app.ErrInvalid) {
		t.Fatal("paid order request accepted")
	}
	req, err := actions.Submit(ctx, key, snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Owned(ctx, viewer, req.ID); err == nil {
		t.Fatal("other identity read request")
	}
	if _, err = actions.Owned(ctx, key, req.ID); err != nil {
		t.Fatal(err)
	}
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := actions.Claim(ctx, req.ID, 8)
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("cancellation approved %d times", wins)
	}
	if _, err = actions.Claim(ctx, req.ID, 8); !errors.Is(err, app.ErrOrderCancellationUnavailable) {
		t.Fatal("replayed claim")
	}
	if err = actions.Complete(ctx, req.ID, domain.ActionSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if err = actions.Complete(ctx, req.ID, domain.ActionSucceeded, ""); err == nil {
		t.Fatal("double complete accepted")
	}
	final, err := actions.Owned(ctx, key, req.ID)
	if err != nil || final.Status != domain.ActionSucceeded {
		t.Fatalf("bad state %+v %v", final, err)
	}
	entries, err := keys.Audits(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	actionsSeen := map[string]bool{}
	for _, v := range entries {
		actionsSeen[v.Action] = true
	}
	for _, action := range []string{"order_cancel_request", "order_cancel_approve", "order_cancel_complete"} {
		if !actionsSeen[action] {
			t.Fatalf("missing audit %s", action)
		}
	}
}
func TestAICancellationRevocationRotationAndExpiry(t *testing.T) {
	db, keys, actions := cancelRequestsFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "expired", []string{app.ScopeOrderCancelRequest}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := actions.Submit(ctx, key, pendingOrderCancellationSnap())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&domain.OrderCancellation{}).Where("id=?", req.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Claim(ctx, req.ID, 8); !errors.Is(err, app.ErrOrderCancellationUnavailable) {
		t.Fatal("expired approved")
	}
	next, err := actions.Submit(ctx, key, pendingOrderCancellationSnap())
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Revoke(ctx, key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = actions.Claim(ctx, next.ID, 8); !errors.Is(err, app.ErrOrderCancellationUnavailable) {
		t.Fatal("revoked request approved")
	}
	rot, _, err := keys.Create(ctx, "rotate", []string{app.ScopeOrderCancelRequest}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := actions.Submit(ctx, rot, pendingOrderCancellationSnap())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keys.Rotate(ctx, rot.ID, 1); err != nil {
		t.Fatal(err)
	}
	// An old in-flight bearer cannot create a new request after rotation.
	if _, err = actions.Submit(ctx, rot, pendingOrderCancellationSnap()); err == nil {
		t.Fatal("rotated old credential created a new request")
	}
	got, err := actions.Owned(ctx, rot, old.ID)
	if err != nil || got.Status != domain.ActionRejected || got.FailureCode != "key_rotated" {
		t.Fatalf("rotated proposal executable %+v %v", got, err)
	}
	if _, err = actions.Claim(ctx, old.ID, 8); !errors.Is(err, app.ErrOrderCancellationUnavailable) {
		t.Fatal("old token request approved")
	}
}
func TestAICancellationAtomicAuditRollback(t *testing.T) {
	db, keys, actions := cancelRequestsFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "test", []string{app.ScopeOrderCancelRequest}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Callback().Create().Before("gorm:create").Register("reject_cancel_audit", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "ai_access_audit_logs" {
			tx.AddError(fmt.Errorf("audit unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("reject_cancel_audit")
	if _, err = actions.Submit(ctx, key, pendingOrderCancellationSnap()); err == nil {
		t.Fatal("proposal persisted without audit")
	}
	var count int64
	if err = db.Model(&domain.OrderCancellation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan cancellation %d %v", count, err)
	}
}
