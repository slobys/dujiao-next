package aiaccessactions

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dujiao-next/internal/app/container"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	productadmin "github.com/dujiao-next/internal/modules/catalog/product/application/admin"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/gin-gonic/gin"
)

type productReader interface {
	GetAdminByID(string) (*productdomain.Product, error)
}
type productWriter interface {
	UpdateStatusIfUnchanged(string, time.Time, bool, string, bool) (*productdomain.Product, error)
}
type Admin struct {
	services *container.Container
	reader   productReader
	writer   productWriter
}

func New(services *container.Container) *Admin {
	return &Admin{
		services: services, reader: services.ProductReadService, writer: services.ProductAdminService,
	}
}
func RegisterAdminRoutes(group gin.IRoutes, services *container.Container) {
	h := New(services)
	group.GET("/ai-access/actions", h.List)
	group.POST("/ai-access/actions/:id/approve", h.Approve)
	group.POST("/ai-access/actions/:id/reject", h.Reject)
}
func actor(c *gin.Context) uint {
	v, _ := c.Get("admin_id")
	if id, ok := v.(uint); ok {
		return id
	}
	return 0
}
func fail(c *gin.Context, status int, msg string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"status_code": status, "msg": msg, "data": nil})
}
func (h *Admin) List(c *gin.Context) {
	items, err := h.services.AiActionService.List(c.Request.Context(), 100)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "Unable to fetch AI approval queue")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, items)
}
func (h *Admin) Reject(c *gin.Context) {
	if actor(c) == 0 {
		fail(c, 403, "AI approval requires authorized administrator")
		return
	}
	err := h.services.AiActionService.Reject(c.Request.Context(), c.Param("id"), actor(c))
	if err != nil {
		if errors.Is(err, aiapp.ErrActionNotAvailable) {
			fail(c, 409, "Request is not pending")
			return
		}
		fail(c, 503, "Could not reject the AI request")
		return
	}
	response.Success(c, gin.H{"status": aidomain.ActionRejected})
}

// Approve is deliberately explicit human-controlled execution. A queue
// request only authorizes a narrowly-defined product status change, never
// arbitrary shell/SQL, refunds or payment-channel actions.
func (h *Admin) Approve(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if actor(c) == 0 {
		fail(c, 403, "AI approval requires authorized administrator")
		return
	}
	if _, err := h.services.AiRemoteService.Active(ctx); err != nil {
		fail(c, 409, "Remote AI access is disabled")
		return
	}
	action, err := h.services.AiActionService.Claim(ctx, id, actor(c))
	if err != nil {
		if errors.Is(err, aiapp.ErrActionNotAvailable) {
			fail(c, 409, "Request is not pending, authorized or unexpired")
			return
		}
		fail(c, 503, "Could not claim AI approval")
		return
	}
	if action == nil || action.Action != aidomain.ActionProductStatus {
		_ = h.services.AiActionService.Complete(ctx, id, aidomain.ActionFailed, "unsupported_action")
		fail(c, 409, "Unsupported AI operation")
		return
	}
	productID := strconv.FormatUint(uint64(action.ProductID), 10)
	original, err := h.reader.GetAdminByID(productID)
	if err != nil || original == nil {
		if e := h.services.AiActionService.Complete(ctx, id, aidomain.ActionConflict, "product_missing"); e != nil {
			fail(c, 503, "Manual review required: status recording failed")
			return
		}
		fail(c, 409, "Product no longer exists")
		return
	}
	// Prices, active flag AND Last Updated timestamp must match the snapshot
	// that the human just approved. Changes after the AI proposal fail closed.
	if original.IsActive != action.ExpectedActive ||
		original.PriceAmount.String() != action.ExpectedPrice ||
		!original.UpdatedAt.Equal(action.ExpectedUpdatedAt) {
		if e := h.services.AiActionService.Complete(ctx, id, aidomain.ActionConflict, "stale_product"); e != nil {
			fail(c, 503, "Manual review required: status recording failed")
			return
		}
		fail(c, 409, "Product changed after AI submitted request")
		return
	}
	updated, err := h.writer.UpdateStatusIfUnchanged(productID, action.ExpectedUpdatedAt, action.ExpectedActive, action.ExpectedPrice, action.DesiredActive)
	if err != nil || updated == nil || updated.IsActive != action.DesiredActive {
		// A concurrent edit may win the optimistic UPDATE after our first
		// snapshot check. That is a conflict, never a reason to overwrite it.
		kind := aidomain.ActionFailed
		code := "product_update_failed"
		if errors.Is(err, productadmin.ErrAIProductChanged) {
			kind = aidomain.ActionConflict
			code = "stale_product"
		}
		if e := h.services.AiActionService.Complete(ctx, id, kind, code); e != nil {
			fail(c, 503, "Manual review required: status recording failed")
			return
		}
		fail(c, 503, "Product update failed; request will not be retried")
		return
	}
	// If this audit write fails, the request stays 'executing' deliberately.
	// Retrying it would risk applying side effects twice after a crash.
	if err = h.services.AiActionService.Complete(ctx, id, aidomain.ActionSucceeded, ""); err != nil {
		fail(c, 503, "Product changed but audit failed: reconcile manually")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{
		"request_id": id, "product_id": action.ProductID,
		"is_active": updated.IsActive, "status": aidomain.ActionSucceeded,
		"approved_at": time.Now().UTC(),
	})
}
