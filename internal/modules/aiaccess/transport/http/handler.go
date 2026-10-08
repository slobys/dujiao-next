package aiaccesshttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dujiao-next/internal/i18n"
	"github.com/dujiao-next/internal/logger"
	"github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/gin-gonic/gin"
)

type AdminHandler struct{ service *application.Service }

func NewAdminHandler(service *application.Service) *AdminHandler {
	return &AdminHandler{service: service}
}

func public(key *domain.Key) gin.H {
	return gin.H{
		"id": key.ID, "name": key.Name, "key_id": key.KeyID,
		"scopes": strings.Split(key.Scopes, ","), "created_by": key.CreatedBy,
		"expires_at": key.ExpiresAt, "revoked_at": key.RevokedAt, "last_used_at": key.LastUsedAt,
		"created_at": key.CreatedAt,
	}
}
func bad(c *gin.Context, err error) {
	code := response.CodeInternal
	key := "error.internal_error"
	if errors.Is(err, application.ErrInvalid) {
		code = response.CodeBadRequest
		key = "error.bad_request"
	}
	if errors.Is(err, application.ErrNotFound) {
		code = response.CodeNotFound
		key = "error.api_credential_not_found"
	}
	ginutil.RespondError(c, code, key, nil)
}

func (h *AdminHandler) List(c *gin.Context) {
	keys, err := h.service.List(c.Request.Context())
	if err != nil {
		bad(c, err)
		return
	}
	result := make([]gin.H, 0, len(keys))
	for i := range keys {
		result = append(result, public(&keys[i]))
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result)
}

type CreateInput struct {
	Name   string   `json:"name" binding:"required"`
	Scopes []string `json:"scopes" binding:"required"`
	Days   int      `json:"days" binding:"required"`
}

func adminID(c *gin.Context) uint {
	raw, ok := c.Get("admin_id")
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case uint:
		return v
	case int:
		if v > 0 {
			return uint(v)
		}
	}
	return 0
}
func (h *AdminHandler) Create(c *gin.Context) {
	var request CreateInput
	if err := c.ShouldBindJSON(&request); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	key, token, err := h.service.Create(c.Request.Context(), request.Name, request.Scopes, request.Days, adminID(c))
	if err != nil {
		bad(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"credential": public(key), "token": token})
}
func (h *AdminHandler) Rotate(c *gin.Context) {
	id, err := ginutil.ParseParamUint(c, "id")
	if err != nil {
		bad(c, application.ErrInvalid)
		return
	}
	token, err := h.service.Rotate(c.Request.Context(), id, adminID(c))
	if err != nil {
		bad(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"token": token})
}
func (h *AdminHandler) Revoke(c *gin.Context) {
	id, err := ginutil.ParseParamUint(c, "id")
	if err != nil {
		bad(c, application.ErrInvalid)
		return
	}
	if err = h.service.Revoke(c.Request.Context(), id, adminID(c)); err != nil {
		bad(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}
func (h *AdminHandler) Audits(c *gin.Context) {
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			bad(c, application.ErrInvalid)
			return
		}
		limit = v
	}
	items, err := h.service.Audits(c.Request.Context(), limit)
	if err != nil {
		bad(c, err)
		return
	}
	response.Success(c, items)
}
func RegisterAdminRoutes(admin gin.IRoutes, h *AdminHandler) {
	admin.GET("/ai-access/keys", h.List)
	admin.POST("/ai-access/keys", h.Create)
	admin.POST("/ai-access/keys/:id/rotate", h.Rotate)
	admin.POST("/ai-access/keys/:id/revoke", h.Revoke)
	admin.GET("/ai-access/audit", h.Audits)
}

// MachineMiddleware never grants any admin identity. Each route explicitly
// requires a single scope, even when the credential has several scopes.
func MachineMiddleware(service *application.Service, scope, route string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || len(header) > 150 {
			response.ErrorWithHTTPStatus(c, http.StatusUnauthorized, response.CodeUnauthorized, i18n.T(i18n.ResolveLocale(c), "error.unauthorized"))
			return
		}
		key, err := service.Authenticate(c.Request.Context(), strings.TrimPrefix(header, "Bearer "), scope)
		if err != nil {
			status := http.StatusUnauthorized
			if !errors.Is(err, application.ErrNotAuthorized) {
				status = http.StatusServiceUnavailable
			}
			response.ErrorWithHTTPStatus(c, status, response.CodeUnauthorized, i18n.T(i18n.ResolveLocale(c), "error.unauthorized"))
			return
		}
		// Guaranteed pre-access audit: if DB unavailable, fail closed.
		if err = service.RecordUse(c.Request.Context(), key, route, "authorized"); err != nil {
			response.ErrorWithHTTPStatus(c, http.StatusServiceUnavailable, response.CodeInternal, i18n.T(i18n.ResolveLocale(c), "error.unauthorized"))
			return
		}
		c.Next()
		if err = service.RecordUse(c.Request.Context(), key, route, strconv.Itoa(c.Writer.Status())); err != nil {
			logger.Warnw("ai_audit_write_failed", "path", route, "error", err)
		}
	}
}
