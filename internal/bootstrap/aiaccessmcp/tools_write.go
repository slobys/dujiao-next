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
