package aiaccesshttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"
	"github.com/gin-gonic/gin"
)

type RemoteAdminHandler struct {
	service *application.RemoteService
}

func NewRemoteAdminHandler(service *application.RemoteService) *RemoteAdminHandler {
	return &RemoteAdminHandler{service: service}
}

type RemoteConfigInput struct {
	Enabled      bool   `json:"enabled"`
	PublicOrigin string `json:"public_origin"`
}

func (h *RemoteAdminHandler) Config(c *gin.Context) {
	v, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		bad(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, v)
}
func (h *RemoteAdminHandler) Update(c *gin.Context) {
	var req RemoteConfigInput
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	v, err := h.service.SetConfig(c.Request.Context(), req.Enabled, strings.TrimSpace(req.PublicOrigin))
	if err != nil {
		bad(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, v)
}
func (h *RemoteAdminHandler) Pending(c *gin.Context) {
	req, client, err := h.service.Pending(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, application.ErrOAuthGrant) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		bad(c, err)
		return
	}
	response.Success(c, gin.H{
		"id": req.ID, "client_id": client.ClientID, "client_name": client.Name,
		"redirect_uri": req.RedirectURI, "scopes": strings.Split(req.Scopes, ","),
		"expires_at": req.ExpiresAt,
	})
}

type ApproveOAuthInput struct {
	RequestID string   `json:"request_id" binding:"required"`
	Scopes    []string `json:"scopes" binding:"required"`
}

func (h *RemoteAdminHandler) Approve(c *gin.Context) {
	var input ApproveOAuthInput
	if err := c.ShouldBindJSON(&input); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	redirect, err := h.service.Approve(c.Request.Context(), input.RequestID, adminID(c), input.Scopes)
	if err != nil {
		bad(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"redirect_url": redirect})
}

type DenyOAuthInput struct {
	RequestID string `json:"request_id" binding:"required"`
}

func (h *RemoteAdminHandler) Deny(c *gin.Context) {
	var input DenyOAuthInput
	if err := c.ShouldBindJSON(&input); err != nil {
		ginutil.RespondBindError(c, err)
		return
	}
	redirect, err := h.service.Deny(c.Request.Context(), input.RequestID, adminID(c))
	if err != nil {
		bad(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"redirect_url": redirect})
}
func RegisterRemoteAdminRoutes(group gin.IRoutes, h *RemoteAdminHandler) {
	group.GET("/ai-access/remote", h.Config)
	group.PUT("/ai-access/remote", h.Update)
	group.GET("/ai-access/oauth/requests/:id", h.Pending)
	group.POST("/ai-access/oauth/approve", h.Approve)
	group.POST("/ai-access/oauth/deny", h.Deny)
}
