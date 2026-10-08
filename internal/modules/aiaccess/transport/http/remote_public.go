package aiaccesshttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/dujiao-next/internal/modules/aiaccess/application"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
	"github.com/gin-gonic/gin"
)

type OAuthPublicHandler struct {
	service   *application.RemoteService
	adminPath string
}

func NewOAuthPublicHandler(service *application.RemoteService, adminPath string) *OAuthPublicHandler {
	if adminPath == "" {
		adminPath = "/admin"
	}
	return &OAuthPublicHandler{service: service, adminPath: adminPath}
}
func oauthError(c *gin.Context, status int, code string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": code})
}
func (h *OAuthPublicHandler) Active(c *gin.Context) (*domain.RemoteConfig, bool) {
	config, err := h.service.Active(c.Request.Context())
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	u, _ := url.Parse(config.PublicOrigin)
	// Do not reflect arbitrary Host or forwarded values in OAuth metadata.
	if !strings.EqualFold(c.Request.Host, u.Host) {
		c.AbortWithStatus(http.StatusMisdirectedRequest)
		return nil, false
	}
	// Our Caddy HTTPS termination sets X-Forwarded-Proto; direct HTTP is refused.
	if !application.SecureProxyRequest(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return nil, false
	}
	return config, true
}
func (h *OAuthPublicHandler) ResourceMetadata(c *gin.Context) {
	cfg, ok := h.Active(c)
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"resource":                 cfg.PublicOrigin + "/mcp",
		"authorization_servers":    []string{cfg.PublicOrigin},
		"scopes_supported":         application.DefaultOAuthScopes(),
		"bearer_methods_supported": []string{"header"},
	})
}
func (h *OAuthPublicHandler) ServerMetadata(c *gin.Context) {
	cfg, ok := h.Active(c)
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                         cfg.PublicOrigin,
		"authorization_endpoint":                         cfg.PublicOrigin + "/oauth/authorize",
		"token_endpoint":                                 cfg.PublicOrigin + "/oauth/token",
		"registration_endpoint":                          cfg.PublicOrigin + "/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
		"scopes_supported":                               application.DefaultOAuthScopes(),
		"client_id_metadata_document_supported":          false,
	})
}

type ClientRegistration struct {
	Name            string   `json:"client_name"`
	RedirectURIs    []string `json:"redirect_uris"`
	TokenAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes      []string `json:"grant_types"`
	ResponseTypes   []string `json:"response_types"`
}

func (h *OAuthPublicHandler) Register(c *gin.Context) {
	_, ok := h.Active(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var input ClientRegistration
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	if input.TokenAuthMethod != "" && input.TokenAuthMethod != "none" {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	for _, t := range input.GrantTypes {
		if t != "authorization_code" && t != "refresh_token" {
			oauthError(c, http.StatusBadRequest, "invalid_client_metadata")
			return
		}
	}
	for _, t := range input.ResponseTypes {
		if t != "code" {
			oauthError(c, http.StatusBadRequest, "invalid_client_metadata")
			return
		}
	}
	client, err := h.service.RegisterClient(c.Request.Context(), input.Name, input.RedirectURIs)
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{
		"client_id": client.ClientID, "client_name": client.Name,
		"redirect_uris":              input.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}
func (h *OAuthPublicHandler) Authorize(c *gin.Context) {
	_, ok := h.Active(c)
	if !ok {
		return
	}
	if c.Query("response_type") != "code" || c.Query("code_challenge_method") != "S256" {
		oauthError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	pending, err := h.service.NewAuthorization(c.Request.Context(),
		c.Query("client_id"), c.Query("redirect_uri"), c.Query("code_challenge"),
		c.Query("state"), c.Query("scope"), c.Query("resource"))
	if err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, h.adminPath+"/ai-authorize?request="+url.QueryEscape(pending.ID))
}
func (h *OAuthPublicHandler) Token(c *gin.Context) {
	_, ok := h.Active(c)
	if !ok {
		return
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/x-www-form-urlencoded") {
		oauthError(c, http.StatusUnsupportedMediaType, "invalid_request")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if err := c.Request.ParseForm(); err != nil {
		oauthError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	var output *application.OAuthTokenResponse
	var err error
	switch c.PostForm("grant_type") {
	case "authorization_code":
		output, err = h.service.Exchange(c.Request.Context(), c.PostForm("client_id"),
			c.PostForm("code"), c.PostForm("redirect_uri"), c.PostForm("code_verifier"), c.PostForm("resource"))
	case "refresh_token":
		output, err = h.service.Refresh(c.Request.Context(), c.PostForm("client_id"),
			c.PostForm("refresh_token"), c.PostForm("resource"))
	default:
		oauthError(c, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		if !errors.Is(err, application.ErrOAuthGrant) {
			oauthError(c, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
		oauthError(c, http.StatusBadRequest, "invalid_grant")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, output)
}
