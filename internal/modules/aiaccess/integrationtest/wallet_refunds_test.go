package integrationtest

import (
	"context"
	"errors"
	"fmt"
	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"gorm.io/gorm"
	"sync"
	"testing"
	"time"
)

func walletApprovalsFixture(t *testing.T) (*gorm.DB, *app.Service, *app.WalletRefunds) {
	t.Helper()
	db, keys := setup(t)
	return db, keys, app.NewWalletRefunds(gormstore.New(db))
}
func walletSnapshot() app.WalletRefundSnapshot {
	return app.WalletRefundSnapshot{
		OrderID: 700, OrderNo: "TEST-WALLET-700", Status: "paid", Currency: "CNY",
		Total: "80.00", Refunded: "0.00", UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
}
func TestAIWalletRefundRequestScopeAmountAuditAndClaimOnce(t *testing.T) {
	_, keys, requests := walletApprovalsFixture(t)
	ctx := context.Background()
	viewer, _, err := keys.Create(ctx, "viewer", []string{app.ScopeOrdersRead}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = requests.Submit(ctx, viewer, walletSnapshot(), "20.00", "customer_request"); !errors.Is(err, app.ErrNotAuthorized) {
		t.Fatal("read-only AI created money-moving proposal")
	}
	key, _, err := keys.Create(ctx, "refund requester", []string{app.ScopeWalletRefundRequest}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, amount := range []string{"-1.00", "0.00", "0.001", "500.01", "20000000000000"} {
		if _, err = requests.Submit(ctx, key, walletSnapshot(), amount, "customer_request"); !errors.Is(err, app.ErrInvalid) {
			t.Fatalf("invalid amount %s allowed: %v", amount, err)
		}
	}
	for _, reason := range []string{"", "send_money_now", "shell", "<script>", "bank_transfer"} {
		if _, err = requests.Submit(ctx, key, walletSnapshot(), "20.00", reason); !errors.Is(err, app.ErrInvalid) {
			t.Fatalf("invalid reason %s allowed: %v", reason, err)
		}
	}
	request, err := requests.Submit(ctx, key, walletSnapshot(), "20.00", "undelivered")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = requests.Owned(ctx, viewer, request.ID); err == nil {
		t.Fatal("different AI read refund request")
	}
	if request.Status != domain.ActionPending || request.Amount != "20.00" {
		t.Fatalf("bad request %+v", request)
	}
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := requests.Claim(ctx, request.ID, 9)
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("refund claimed %d times", wins)
	}
	if _, err = requests.Claim(ctx, request.ID, 9); !errors.Is(err, app.ErrWalletRefundUnavailable) {
		t.Fatal("duplicate approval allowed")
	}
	if err = requests.Complete(ctx, request.ID, domain.ActionSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if err = requests.Complete(ctx, request.ID, domain.ActionSucceeded, ""); err == nil {
		t.Fatal("completed twice")
	}
	log, err := keys.Audits(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range log {
		seen[entry.Action] = true
	}
	for _, event := range []string{"ai_wallet_refund_request", "ai_wallet_refund_approve", "ai_wallet_refund_complete"} {
		if !seen[event] {
			t.Fatalf("missing audit %s", event)
		}
	}
}
func TestAIWalletRefundPendingExpiresRevokesAndRotates(t *testing.T) {
	db, keys, requests := walletApprovalsFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "AI", []string{app.ScopeWalletRefundRequest}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := requests.Submit(ctx, key, walletSnapshot(), "10.00", "customer_request")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&domain.WalletRefundRequest{}).Where("id=?", expired.ID).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = requests.Claim(ctx, expired.ID, 9); !errors.Is(err, app.ErrWalletRefundUnavailable) {
		t.Fatal("expired refund approval succeeded")
	}
	rev, err := requests.Submit(ctx, key, walletSnapshot(), "10.00", "customer_request")
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Revoke(ctx, key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = requests.Claim(ctx, rev.ID, 9); !errors.Is(err, app.ErrWalletRefundUnavailable) {
		t.Fatal("revoked AI refund request approved")
	}
	rotatedKey, _, err := keys.Create(ctx, "AI Rotating", []string{app.ScopeWalletRefundRequest}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := requests.Submit(ctx, rotatedKey, walletSnapshot(), "10.00", "customer_request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keys.Rotate(ctx, rotatedKey.ID, 1); err != nil {
		t.Fatal(err)
	}
	state, err := requests.Owned(ctx, rotatedKey, old.ID)
	if err != nil || state.Status != domain.ActionRejected || state.FailureCode != "key_rotated" {
		t.Fatalf("pending refund survives key rotation %+v %v", state, err)
	}
	if _, err = requests.Submit(ctx, rotatedKey, walletSnapshot(), "5.00", "other"); err == nil {
		t.Fatal("rotated stale bearer created refund")
	}
}
func TestAIWalletRefundAuditFailureRollsBack(t *testing.T) {
	db, keys, requests := walletApprovalsFixture(t)
	ctx := context.Background()
	key, _, err := keys.Create(ctx, "AI", []string{app.ScopeWalletRefundRequest}, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Callback().Create().Before("gorm:create").Register("reject_wallet_refund_audit", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "ai_access_audit_logs" {
			tx.AddError(fmt.Errorf("audit unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("reject_wallet_refund_audit")
	if _, err = requests.Submit(ctx, key, walletSnapshot(), "10.00", "customer_request"); err == nil {
		t.Fatal("unlogged refund request saved")
	}
	var count int64
	if err = db.Model(&domain.WalletRefundRequest{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan refund %d %v", count, err)
	}
}
