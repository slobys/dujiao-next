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

func reviewFixture(t *testing.T) (*gorm.DB, *app.Service, *app.OrderReviews) {
	t.Helper()
	db, keys := setup(t)
	if err := db.AutoMigrate(&domain.OrderReview{}); err != nil {
		t.Fatal(err)
	}
	return db, keys, app.NewOrderReviews(gormstore.New(db))
}
func exampleOrder() app.OrderSnapshot {
	return app.OrderSnapshot{
		ID: 251, OrderNo: "DJ-2026-TEST-01", Status: "paid",
		Currency: "CNY", Total: "79.90", UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
}
func TestOrderReviewRequiresDedicatedScopeAndReason(t *testing.T) {
	_, keys, review := reviewFixture(t)
	ctx := context.Background()
	read, _, err := keys.Create(ctx, "orders viewer", []string{app.ScopeOrdersRead}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = review.Submit(ctx, read, exampleOrder(), "refund_review"); !errors.Is(err, app.ErrNotAuthorized) {
		t.Fatal("read-only account submitted aftersales action")
	}
	caller, _, err := keys.Create(ctx, "after-sales", []string{app.ScopeOrderReviewRequest}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"refund_now", "refund:execute", "shell", "", "manual\ncancel"} {
		if _, err = review.Submit(ctx, caller, exampleOrder(), code); !errors.Is(err, app.ErrInvalid) {
			t.Fatalf("accepted dangerous reason %q: %v", code, err)
		}
	}
	for _, code := range []string{"refund_review", "payment_exception", "delivery_delay", "cancellation_request", "other_exception"} {
		item, err := review.Submit(ctx, caller, exampleOrder(), code)
		if err != nil || item.Status != domain.OrderReviewPending || !item.ExpiresAt.After(item.CreatedAt) {
			t.Fatalf("invalid item %+v %v", item, err)
		}
		if _, err = review.Owned(ctx, read, item.ID); err == nil {
			t.Fatal("other AI key read private work item")
		}
		if _, err = review.Owned(ctx, caller, item.ID); err != nil {
			t.Fatal(err)
		}
	}
}
func TestOrderReviewHumanWorkflowAndAudit(t *testing.T) {
	_, keys, review := reviewFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "Codex", []string{app.ScopeOrderReviewRequest}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	item, err := review.Submit(ctx, key, exampleOrder(), "refund_review")
	if err != nil {
		t.Fatal(err)
	}
	var ok int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, e := review.Process(ctx, item.ID, 7, "accept")
			if e == nil && status == domain.OrderReviewAccepted {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("double accepted %d times", ok)
	}
	if _, err = review.Process(ctx, item.ID, 7, "accept"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatal("repeated accept")
	}
	if status, err := review.Process(ctx, item.ID, 7, "resolve"); err != nil || status != domain.OrderReviewResolved {
		t.Fatalf("failed to mark human follow-up completed %s %v", status, err)
	}
	if _, err = review.Process(ctx, item.ID, 7, "resolve"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatal("resolved twice")
	}
	records, err := keys.Audits(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, entry := range records {
		counts[entry.Action]++
	}
	if counts["order_review_request"] != 1 || counts["order_review_accept"] != 1 || counts["order_review_resolve"] != 1 {
		t.Fatalf("missing audit %+v", counts)
	}
}
func TestOrderReviewRevocationExpiryAndRejection(t *testing.T) {
	db, keys, review := reviewFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "OpenClaw", []string{app.ScopeOrderReviewRequest}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	item, err := review.Submit(ctx, key, exampleOrder(), "cancellation_request")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&domain.OrderReview{}).Where("id=?", item.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = review.Process(ctx, item.ID, 8, "accept"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatal("expired request accepted")
	}
	another, err := review.Submit(ctx, key, exampleOrder(), "payment_exception")
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Revoke(ctx, key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = review.Process(ctx, another.ID, 8, "accept"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatal("revoked AI account accepted")
	}
	if state, err := review.GetForAdmin(ctx, another.ID); err != nil || state.Status != domain.OrderReviewRejected {
		t.Fatalf("revocation did not reject pending review: %+v %v", state, err)
	}
	if _, err = review.Process(ctx, another.ID, 8, "resolve"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatal("rejected item resolved")
	}
}
func TestRotationCancelsPendingOrderReview(t *testing.T) {
	_, keys, review := reviewFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "Old Agent", []string{app.ScopeOrderReviewRequest}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	item, err := review.Submit(ctx, key, exampleOrder(), "refund_review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keys.Rotate(ctx, key.ID, 1); err != nil {
		t.Fatal(err)
	}
	state, err := review.GetForAdmin(ctx, item.ID)
	if err != nil || state.Status != domain.OrderReviewRejected {
		t.Fatalf("rotated key still has pending review: %+v %v", state, err)
	}
	if _, err = review.Process(ctx, item.ID, 2, "accept"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatal("rotated request approved")
	}
}

func TestOrderReviewAuditFailureRollsBack(t *testing.T) {
	db, keys, review := reviewFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "OpenClaw", []string{app.ScopeOrderReviewRequest}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Callback().Create().Before("gorm:create").Register("reject_review_audit", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "ai_access_audit_logs" {
			tx.AddError(fmt.Errorf("auditing unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("reject_review_audit")
	if _, err = review.Submit(ctx, key, exampleOrder(), "delivery_delay"); err == nil {
		t.Fatal("created request without audit")
	}
	var count int64
	if err = db.Model(&domain.OrderReview{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("left orphan review %d %v", count, err)
	}
}
