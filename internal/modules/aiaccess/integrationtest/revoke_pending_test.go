package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	app "github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"gorm.io/gorm"
)

// Revoking a credential must retire its unclaimed proposals across queues,
// while leaving already claimed work and other identities untouched.
func TestAIRevokeRejectsOnlyOwnedPendingProposals(t *testing.T) {
	db, keys := setup(t)
	store := gormstore.New(db)
	actions := app.NewActions(store)
	reviews := app.NewOrderReviews(store)
	cancels := app.NewOrderCancellations(store)
	refunds := app.NewWalletRefunds(store)
	ctx := context.Background()
	scopes := []string{app.ScopePublishRequest, app.ScopeOrderReviewRequest, app.ScopeOrderCancelRequest, app.ScopeWalletRefundRequest}
	key, token, err := keys.Create(ctx, "OpenClaw", scopes, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	other, otherToken, err := keys.Create(ctx, "Codex", scopes, 5, 1)
	if err != nil {
		t.Fatal(err)
	}

	action, err := actions.SubmitStatus(ctx, key, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviews.Submit(ctx, key, exampleOrder(), "payment_exception")
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := cancels.Submit(ctx, key, pendingOrderCancellationSnap())
	if err != nil {
		t.Fatal(err)
	}
	refund, err := refunds.Submit(ctx, key, walletSnapshot(), "10.00", "undelivered")
	if err != nil {
		t.Fatal(err)
	}

	otherAction, err := actions.SubmitStatus(ctx, other, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	executing, err := refunds.Submit(ctx, key, walletSnapshot(), "5.00", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.WalletRefundRequest{}).Where("id = ?", executing.ID).Update("status", domain.ActionExecuting).Error; err != nil {
		t.Fatal(err)
	}
	accepted, err := reviews.Submit(ctx, key, exampleOrder(), "delivery_delay")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.OrderReview{}).Where("id = ?", accepted.ID).Update("status", domain.OrderReviewAccepted).Error; err != nil {
		t.Fatal(err)
	}

	if err = keys.Revoke(ctx, key.ID, 9); err != nil {
		t.Fatal(err)
	}

	gotAction, err := actions.Owned(ctx, key, action.ID)
	if err != nil || gotAction.Status != domain.ActionRejected || gotAction.FailureCode != "key_revoked" {
		t.Fatalf("action %+v, %v", gotAction, err)
	}
	gotReview, err := reviews.GetForAdmin(ctx, review.ID)
	if err != nil || gotReview.Status != domain.OrderReviewRejected {
		t.Fatalf("review %+v, %v", gotReview, err)
	}
	gotCancel, err := cancels.GetForAdmin(ctx, cancel.ID)
	if err != nil || gotCancel.Status != domain.ActionRejected || gotCancel.FailureCode != "key_revoked" {
		t.Fatalf("cancel %+v, %v", gotCancel, err)
	}
	gotRefund, err := refunds.GetForAdmin(ctx, refund.ID)
	if err != nil || gotRefund.Status != domain.ActionRejected || gotRefund.FailureCode != "key_revoked" {
		t.Fatalf("refund %+v, %v", gotRefund, err)
	}

	stillPending, err := actions.Owned(ctx, other, otherAction.ID)
	if err != nil || stillPending.Status != domain.ActionPending {
		t.Fatalf("other key affected %+v, %v", stillPending, err)
	}
	stillExecuting, err := refunds.GetForAdmin(ctx, executing.ID)
	if err != nil || stillExecuting.Status != domain.ActionExecuting {
		t.Fatalf("executing request changed %+v, %v", stillExecuting, err)
	}
	stillAccepted, err := reviews.GetForAdmin(ctx, accepted.ID)
	if err != nil || stillAccepted.Status != domain.OrderReviewAccepted {
		t.Fatalf("accepted human task changed %+v, %v", stillAccepted, err)
	}

	if _, err = actions.Claim(ctx, action.ID, 9); !errors.Is(err, app.ErrActionNotAvailable) {
		t.Fatalf("action approved: %v", err)
	}
	if _, err = cancels.Claim(ctx, cancel.ID, 9); !errors.Is(err, app.ErrOrderCancellationUnavailable) {
		t.Fatalf("cancel approved: %v", err)
	}
	if _, err = refunds.Claim(ctx, refund.ID, 9); !errors.Is(err, app.ErrWalletRefundUnavailable) {
		t.Fatalf("refund approved: %v", err)
	}
	if _, err = reviews.Process(ctx, review.ID, 9, "accept"); !errors.Is(err, app.ErrOrderReviewUnavailable) {
		t.Fatalf("review accepted: %v", err)
	}
	if _, err = keys.Authenticate(ctx, token, app.ScopePublishRequest); !errors.Is(err, app.ErrNotAuthorized) {
		t.Fatalf("revoked token active: %v", err)
	}
	if _, err = keys.Authenticate(ctx, otherToken, app.ScopePublishRequest); err != nil {
		t.Fatalf("other token blocked: %v", err)
	}
	if err = keys.Revoke(ctx, key.ID, 9); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("duplicate revoke: %v", err)
	}
}

// An MCP request may carry a credential authenticated before the admin
// revoked or rotated it; rechecking at persistence must fail closed.
func TestStaleAIKeyCannotCreateFreshProposals(t *testing.T) {
	for _, change := range []string{"revoke", "rotate"} {
		t.Run(change, func(t *testing.T) {
			db, keys := setup(t)
			store := gormstore.New(db)
			actions := app.NewActions(store)
			reviews := app.NewOrderReviews(store)
			ctx := context.Background()
			key, _, err := keys.Create(ctx, "Old OpenClaw", []string{
				app.ScopePublishRequest, app.ScopeOrderReviewRequest,
			}, 5, 1)
			if err != nil {
				t.Fatal(err)
			}
			if change == "revoke" {
				err = keys.Revoke(ctx, key.ID, 1)
			} else {
				_, err = keys.Rotate(ctx, key.ID, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			// key is intentionally the original in-memory snapshot, not a
			// fresh credential loaded from the database.
			if _, err = actions.SubmitStatus(ctx, key, actionSnapshot(true)); err == nil {
				t.Fatal("stale key created a new product status proposal")
			}
			if _, err = reviews.Submit(ctx, key, exampleOrder(), "payment_exception"); err == nil {
				t.Fatal("stale key created a new order review")
			}
			var actionCount, reviewCount int64
			if err = db.Model(&domain.ActionRequest{}).Count(&actionCount).Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Model(&domain.OrderReview{}).Count(&reviewCount).Error; err != nil {
				t.Fatal(err)
			}
			if actionCount != 0 || reviewCount != 0 {
				t.Fatalf("stale credential persisted proposals: %d actions, %d reviews", actionCount, reviewCount)
			}
		})
	}
}

func TestAIRevokePendingChangesRollbackWhenAuditFails(t *testing.T) {
	db, keys := setup(t)
	ctx := context.Background()
	store := gormstore.New(db)
	actions := app.NewActions(store)
	reviews := app.NewOrderReviews(store)
	cancels := app.NewOrderCancellations(store)
	refunds := app.NewWalletRefunds(store)
	key, token, err := keys.Create(ctx, "OpenClaw", []string{
		app.ScopePublishRequest, app.ScopeOrderReviewRequest,
		app.ScopeOrderCancelRequest, app.ScopeWalletRefundRequest,
	}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	action, err := actions.SubmitStatus(ctx, key, actionSnapshot(true))
	if err != nil {
		t.Fatal(err)
	}

	review, err := reviews.Submit(ctx, key, exampleOrder(), "payment_exception")
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := cancels.Submit(ctx, key, pendingOrderCancellationSnap())
	if err != nil {
		t.Fatal(err)
	}
	refund, err := refunds.Submit(ctx, key, walletSnapshot(), "10.00", "undelivered")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Callback().Create().Before("gorm:create").Register("fail_revoke_audit", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "ai_access_audit_logs" {
			tx.AddError(fmt.Errorf("simulated audit failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("fail_revoke_audit")
	if err := keys.Revoke(ctx, key.ID, 9); err == nil {
		t.Fatal("revocation committed without an audit")
	}
	proposal, err := actions.Owned(ctx, key, action.ID)
	if err != nil || proposal.Status != domain.ActionPending {
		t.Fatalf("proposal changed on rollback: %+v %v", proposal, err)
	}
	rolledBackReview, err := reviews.GetForAdmin(ctx, review.ID)
	if err != nil || rolledBackReview.Status != domain.OrderReviewPending {
		t.Fatalf("order review changed on rollback: %+v %v", rolledBackReview, err)
	}
	rolledBackCancel, err := cancels.GetForAdmin(ctx, cancel.ID)
	if err != nil || rolledBackCancel.Status != domain.ActionPending {
		t.Fatalf("order cancellation changed on rollback: %+v %v", rolledBackCancel, err)
	}
	rolledBackRefund, err := refunds.GetForAdmin(ctx, refund.ID)
	if err != nil || rolledBackRefund.Status != domain.ActionPending {
		t.Fatalf("wallet refund changed on rollback: %+v %v", rolledBackRefund, err)
	}
	if _, err := keys.Authenticate(ctx, token, app.ScopePublishRequest); err != nil {
		t.Fatalf("token unexpectedly revoked: %v", err)
	}
}
