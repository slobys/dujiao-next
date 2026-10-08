package aiaccessmcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dujiao-next/internal/constants"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	productwrite "github.com/dujiao-next/internal/modules/catalog/product/application/write"
	ordercontract "github.com/dujiao-next/internal/modules/order/contract"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
)

type scopeCheck func(context.Context, string, string) error

var draftSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?$`)

type StatusProposal struct {
	ProductID uint `json:"product_id"`
	Publish   bool `json:"publish"`
}
type ActionLookup struct {
	RequestID string `json:"request_id"`
}

// Plain text only: merchant content is data and can never be interpreted as
// hidden instructions to grant permissions or run code.
func safeText(raw string, max int) bool {
	if len(raw) == 0 || len(raw) > max || !utf8.ValidString(raw) || strings.ContainsAny(raw, "<>") {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func simpleDraftPrice(raw string) (decimal.Decimal, error) {
	if len(raw) < 1 || len(raw) > 22 {
		return decimal.Decimal{}, errors.New("invalid price")
	}
	price, err := decimal.NewFromString(raw)
	if err != nil || price.LessThan(decimal.NewFromFloat(0.01)) ||
		price.GreaterThan(decimal.NewFromInt(999999)) || price.Exponent() < -2 {
		return decimal.Decimal{}, errors.New("invalid price")
	}
	return price, nil
}
func (h *Handler) registerWriteTools(server *mcp.Server, key *aidomain.Key, check scopeCheck) {
	if aiapp.HasScope(key.Scopes, aiapp.ScopeDraftWrite) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "create_product_draft",
			Description: "CREATE A REAL UNPUBLISHED MANUAL-FULFILLMENT PRODUCT DRAFT. Forces zero stock, no payment keys or auto delivery; cannot publish. Requires catalog:draft:write consent.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in Draft) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopeDraftWrite, "create_product_draft"); err != nil {
				return nil, nil, err
			}
			if in.CategoryID == 0 || !safeText(in.Title, 120) ||
				(in.Description != "" && !safeText(in.Description, 1500)) ||
				!draftSlugPattern.MatchString(in.Slug) {
				return nil, nil, errors.New("invalid draft fields")
			}
			amount, err := simpleDraftPrice(in.Price)
			if err != nil {
				return nil, nil, err
			}
			zero := 0
			disabled := false
			created, err := h.services.ProductWriteService.Create(productwrite.CreateProductInput{
				CategoryID: in.CategoryID, Slug: in.Slug,
				TitleJSON:       map[string]interface{}{"zh-CN": in.Title},
				DescriptionJSON: map[string]interface{}{"zh-CN": in.Description},
				PriceAmount:     amount, CostPriceAmount: decimal.Zero,
				PurchaseType:     constants.ProductPurchaseMember,
				FulfillmentType:  constants.FulfillmentTypeManual,
				StockDisplayMode: constants.ProductStockDisplayHidden,
				ManualStockTotal: &zero, IsActive: &disabled,
			})
			if err != nil {
				return nil, nil, errors.New("could not create draft: verify category and unique slug")
			}
			if created == nil || created.IsActive || created.ManualStockTotal != 0 || created.FulfillmentType != constants.FulfillmentTypeManual {
				return nil, nil, errors.New("draft safety check failed; review product in admin")
			}
			return nil, map[string]any{
				"created": true, "published": false, "product_id": created.ID,
				"slug": created.Slug, "stock": 0,
				"next_step": "Admin may edit the draft; AI must request explicit approval to publish it",
			}, nil
		})
	}
	if aiapp.HasScope(key.Scopes, aiapp.ScopePublishRequest) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "request_product_status_change",
			Description: "REQUEST ONLY: submit a product publish/unpublish operation to the admin approval queue. No product changes occur without explicit administrator approval.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in StatusProposal) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopePublishRequest, "request_product_status_change"); err != nil {
				return nil, nil, err
			}
			if in.ProductID == 0 {
				return nil, nil, errors.New("invalid product id")
			}
			product, err := h.services.ProductReadService.GetAdminByID(strconv.FormatUint(uint64(in.ProductID), 10))
			if err != nil || product == nil {
				return nil, nil, errors.New("product unavailable")
			}
			title, _ := product.TitleJSON["zh-CN"].(string)
			if !safeText(title, 240) {
				title = fmt.Sprintf("Product #%d", product.ID)
			}
			proposal := aiapp.ProductStatusSnapshot{
				ProductID: product.ID, Title: title, CurrentActive: product.IsActive,
				DesiredActive: in.Publish, Price: product.PriceAmount.String(),
				UpdatedAt: product.UpdatedAt,
			}
			pending, err := h.services.AiActionService.SubmitStatus(ctx, key, proposal)
			if err != nil {
				return nil, nil, errors.New("could not queue status change: current state or permission invalid")
			}
			return nil, map[string]any{
				"request_id": pending.ID, "product_id": pending.ProductID,
				"requested_publish": pending.DesiredActive, "status": pending.Status,
				"executed": false, "requires_human_approval": true,
				"expires_at": pending.ExpiresAt,
			}, nil
		})
		mcp.AddTool(server, &mcp.Tool{
			Name:        "get_product_action_status",
			Description: "Read the status of your own previously-submitted product publish/unpublish request.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in ActionLookup) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopePublishRequest, "get_product_action_status"); err != nil {
				return nil, nil, err
			}
			action, err := h.services.AiActionService.Owned(ctx, key, in.RequestID)
			if err != nil {
				return nil, nil, errors.New("action request not found")
			}
			return nil, map[string]any{
				"request_id": action.ID, "status": action.Status,
				"product_id": action.ProductID, "requested_publish": action.DesiredActive,
				"created_at": action.CreatedAt, "expires_at": action.ExpiresAt,
				"completed_at": action.CompletedAt, "failure_code": action.FailureCode,
			}, nil
		})
	}
}

type OrderSearch struct {
	Status   string `json:"status,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}
type OrderByID struct {
	OrderID uint `json:"order_id"`
}
type OrderReviewInput struct {
	OrderID uint   `json:"order_id"`
	Reason  string `json:"reason"`
}
type OrderReviewLookup struct {
	RequestID string `json:"request_id"`
}

func safeOrderStatus(value string) bool {
	switch value {
	case "", constants.OrderStatusPendingPayment, constants.OrderStatusPaid,
		constants.OrderStatusFulfilling, constants.OrderStatusPartiallyDelivered,
		constants.OrderStatusPartiallyRefunded, constants.OrderStatusDelivered,
		constants.OrderStatusCompleted, constants.OrderStatusCanceled,
		constants.OrderStatusRefunded:
		return true
	default:
		return false
	}
}

// Explicit field whitelist. Do NOT pass through the underlying Order, Items,
// Fulfillment, user emails, card secrets, refund records, wallets or IPs.
func summarizeOrder(order *orderdomain.Order) map[string]any {
	return map[string]any{
		"order_id": order.ID, "order_no": order.OrderNo, "status": order.Status,
		"total_amount": order.TotalAmount.String(), "currency": order.Currency,
		"created_at": order.CreatedAt, "updated_at": order.UpdatedAt,
		"paid_at": order.PaidAt, "expires_at": order.ExpiresAt,
	}
}
func snapshotOrder(order *orderdomain.Order) aiapp.OrderSnapshot {
	return aiapp.OrderSnapshot{
		ID: order.ID, OrderNo: order.OrderNo, Status: order.Status,
		Total: order.TotalAmount.String(), Currency: order.Currency, UpdatedAt: order.UpdatedAt,
	}
}
func (h *Handler) registerOrderTools(server *mcp.Server, key *aidomain.Key, check scopeCheck) {
	if aiapp.HasScope(key.Scopes, aiapp.ScopeOrdersRead) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "list_order_summaries",
			Description: "Read-only listing of SAFE merchant order metadata (status, amount, timestamps). NEVER returns customer names/emails, IPs, payment tokens, digital secrets, delivery or refund details.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in OrderSearch) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopeOrdersRead, "list_order_summaries"); err != nil {
				return nil, nil, err
			}
			if !safeOrderStatus(in.Status) {
				return nil, nil, errors.New("invalid order status")
			}
			if in.Page == 0 {
				in.Page = 1
			}
			if in.PageSize == 0 {
				in.PageSize = 20
			}
			if in.Page < 1 || in.Page > 10000 || in.PageSize < 1 || in.PageSize > 25 {
				return nil, nil, errors.New("invalid pagination")
			}
			// Store query is read-only; unlike OrderService.ListOrdersForAdmin it
			// never lazily cancels or updates expired/partially-refunded orders.
			rows, total, err := h.services.OrderStore.ListAdmin(ordercontract.ListFilter{
				Page: in.Page, PageSize: in.PageSize, Status: in.Status,
			})
			if err != nil {
				return nil, nil, errors.New("order query unavailable")
			}
			records := make([]map[string]any, 0, len(rows))
			for i := range rows {
				records = append(records, summarizeOrder(&rows[i]))
			}
			return nil, map[string]any{"orders": records, "total_count": total, "page": in.Page, "page_size": in.PageSize}, nil
		})
		mcp.AddTool(server, &mcp.Tool{
			Name:        "get_order_summary",
			Description: "Read-only, sanitized single order summary. No emails, user IDs, fulfillment/card secrets, phone, address, payment or refund tokens.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in OrderByID) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopeOrdersRead, "get_order_summary"); err != nil {
				return nil, nil, err
			}
			if in.OrderID == 0 {
				return nil, nil, errors.New("invalid order id")
			}
			order, err := h.services.OrderStore.GetByID(in.OrderID)
			if err != nil || order == nil {
				return nil, nil, errors.New("order not found")
			}
			return nil, map[string]any{"order": summarizeOrder(order)}, nil
		})
	}
	if aiapp.HasScope(key.Scopes, aiapp.ScopeOrderReviewRequest) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "request_order_after_sales_review",
			Description: "Submit an order after-sales TRIAGE TICKET only. Reason MUST be refund_review, delivery_delay, payment_exception, cancellation_request, or other_exception. No refund, order cancellation, delivery or payment operation occurs. Human follows up in the merchant order admin.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in OrderReviewInput) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopeOrderReviewRequest, "request_order_after_sales_review"); err != nil {
				return nil, nil, err
			}
			if in.OrderID == 0 {
				return nil, nil, errors.New("invalid order id")
			}
			order, err := h.services.OrderStore.GetByID(in.OrderID)
			if err != nil || order == nil {
				return nil, nil, errors.New("order not found")
			}
			review, err := h.services.AiOrderReviewService.Submit(ctx, key, snapshotOrder(order), in.Reason)
			if err != nil {
				return nil, nil, errors.New("unable to submit review: check reason and permission")
			}
			return nil, map[string]any{
				"request_id": review.ID, "order_id": review.OrderID,
				"reason": review.Reason, "status": review.Status,
				"expires_at": review.ExpiresAt, "order_modified": false,
				"refund_processed": false, "delivery_processed": false,
				"next_step": "A human must accept the triage ticket and use the original merchant order admin to perform any actual refund, cancellation, delivery or payment action",
			}, nil
		})
		mcp.AddTool(server, &mcp.Tool{
			Name:        "get_order_after_sales_review_status",
			Description: "View the status of your own after-sales triage ticket; never exposes other AI agents' review requests.",
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in OrderReviewLookup) (*mcp.CallToolResult, map[string]any, error) {
			if err := check(ctx, aiapp.ScopeOrderReviewRequest, "get_order_after_sales_review_status"); err != nil {
				return nil, nil, err
			}
			review, err := h.services.AiOrderReviewService.Owned(ctx, key, in.RequestID)
			if err != nil {
				return nil, nil, errors.New("review not found")
			}
			return nil, map[string]any{
				"request_id": review.ID, "order_id": review.OrderID, "reason": review.Reason,
				"status": review.Status, "created_at": review.CreatedAt,
				"expires_at": review.ExpiresAt, "reviewed_at": review.ReviewedAt,
				"resolved_at":                         review.ResolvedAt,
				"refund_or_delivery_performed_by_mcp": false,
				"interpretation":                      "A resolved ticket means a human marked the triage task complete, not that the store has automatically refunded or delivered the order",
			}, nil
		})
	}
}
