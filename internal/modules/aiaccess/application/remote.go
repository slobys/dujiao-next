package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dujiao-next/internal/modules/aiaccess/contract"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

var (
	ErrOAuthGrant     = errors.New("invalid or expired OAuth authorization")
	ErrRemoteDisabled = errors.New("remote MCP is not enabled")
	dnsLabel          = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

const (
	AccessTokenTTL   = time.Hour
	RefreshTokenTTL  = 30 * 24 * time.Hour
	AuthorizationTTL = 10 * time.Minute
)

type RemoteService struct {
	repo contract.RemoteRepository
	keys *Service
	now  func() time.Time
}

func NewRemote(repo contract.RemoteRepository, keys *Service) *RemoteService {
	return &RemoteService{repo: repo, keys: keys, now: time.Now}
}
func (s *RemoteService) Now() time.Time { return s.now().UTC() }

// SecureProxyRequest rejects forged HTTPS-forwarded headers from public
// plaintext peers. Caddy's local/private Docker hop is the only HTTP
// exception; direct public HTTP is never accepted for bearer credentials.
func SecureProxyRequest(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	if req.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func HTTPSOrigin(raw string) (string, error) {
	if len(raw) < 9 || len(raw) > 253 || strings.TrimSpace(raw) != raw {
		return "", ErrInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", ErrInvalid
	}
	host := strings.ToLower(parsed.Hostname())
	if len(host) > 253 || net.ParseIP(host) != nil {
		return "", ErrInvalid
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 || !regexp.MustCompile(`^[a-z]{2,63}$`).MatchString(labels[len(labels)-1]) {
		return "", ErrInvalid
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return "", ErrInvalid
		}
	}
	port := parsed.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrInvalid
		}
		if n == 443 {
			port = ""
		}
	}
	if port != "" {
		return "https://" + host + ":" + port, nil
	}
	return "https://" + host, nil
}
func (s *RemoteService) GetConfig(ctx context.Context) (*domain.RemoteConfig, error) {
	return s.repo.GetRemote(ctx)
}
func (s *RemoteService) SetConfig(ctx context.Context, enabled bool, raw string) (*domain.RemoteConfig, error) {
	origin := ""
	if raw != "" {
		var err error
		origin, err = HTTPSOrigin(raw)
		if err != nil {
			return nil, err
		}
	}
	if enabled && origin == "" {
		return nil, ErrInvalid
	}
	config := &domain.RemoteConfig{ID: 1, Enabled: enabled, PublicOrigin: origin}
	if err := s.repo.SaveRemote(ctx, config); err != nil {
		return nil, err
	}
	return s.repo.GetRemote(ctx)
}
func (s *RemoteService) Active(ctx context.Context) (*domain.RemoteConfig, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || cfg.PublicOrigin == "" {
		return nil, ErrRemoteDisabled
	}
	if _, err = HTTPSOrigin(cfg.PublicOrigin); err != nil {
		return nil, ErrRemoteDisabled
	}
	return cfg, nil
}
func randomHex(n int) (string, error) {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
func hashOpaque(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// The client may request the full supported list, but the consent UI must
// default all write/request scopes to unchecked; scope grants are user-controlled.
func DefaultOAuthScopes() []string {
	return []string{ScopeCatalog, ScopeInventory, ScopeReport, ScopeDraftWrite, ScopePublishRequest, ScopeOrdersRead, ScopeOrderReviewRequest}
}
func oauthScopes(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return parseScopes(DefaultOAuthScopes())
	}
	return parseScopes(strings.Fields(raw))
}
func RegisteredRedirect(raw string) error {
	if len(raw) < 12 || len(raw) > 2048 {
		return ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" {
		return ErrInvalid
	}
	if u.Scheme == "http" {
		if !strings.EqualFold(u.Hostname(), "localhost") && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
			return ErrInvalid
		}
		if u.Port() == "" {
			return ErrInvalid
		}
	} else if u.Scheme == "https" {
		// Strict public TLS redirects only. Avoid arbitrary URL schemes and IPs.
		if _, err := HTTPSOrigin("https://" + u.Host); err != nil {
			return ErrInvalid
		}
	} else {
		return ErrInvalid
	}
	return nil
}
func (s *RemoteService) RegisterClient(ctx context.Context, name string, redirects []string) (*domain.OAuthClient, error) {
	name = strings.TrimSpace(name)
	if !validCredentialName(name) || len(redirects) == 0 || len(redirects) > 5 {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range redirects {
		if RegisteredRedirect(r) != nil || seen[r] {
			return nil, ErrInvalid
		}
		seen[r] = true
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	serialized, err := json.Marshal(redirects)
	if err != nil {
		return nil, err
	}
	client := &domain.OAuthClient{ClientID: "djmc_" + id, Name: name, RedirectURIs: string(serialized), CreatedAt: s.Now()}
	if err = s.repo.RegisterClient(ctx, client); err != nil {
		return nil, err
	}
	return client, nil
}
func (s *RemoteService) FindClient(ctx context.Context, id string) (*domain.OAuthClient, error) {
	if len(id) > 42 || !strings.HasPrefix(id, "djmc_") {
		return nil, ErrInvalid
	}
	return s.repo.FindClient(ctx, id)
}
func (s *RemoteService) NewAuthorization(ctx context.Context, clientID, redirect, challenge, state, scopes, resource string) (*domain.AuthorizationRequest, error) {
	cfg, err := s.Active(ctx)
	if err != nil {
		return nil, err
	}
	if resource == "" {
		resource = cfg.PublicOrigin + "/mcp"
	}
	if resource != cfg.PublicOrigin+"/mcp" || len(state) > 512 || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(challenge) {
		return nil, ErrInvalid
	}
	client, err := s.FindClient(ctx, clientID)
	if err != nil || client == nil {
		return nil, ErrInvalid
	}
	var registered []string
	if err = json.Unmarshal([]byte(client.RedirectURIs), &registered); err != nil {
		return nil, err
	}
	allowed := false
	for _, r := range registered {
		if r == redirect {
			allowed = true
			break
		}
	}
	if !allowed || RegisteredRedirect(redirect) != nil {
		return nil, ErrInvalid
	}
	joined, err := oauthScopes(scopes)
	if err != nil {
		return nil, err
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	pending := &domain.AuthorizationRequest{
		ID: id, ClientID: clientID, RedirectURI: redirect, Scopes: joined,
		State: state, CodeChallenge: challenge, Resource: resource,
		ExpiresAt: s.Now().Add(AuthorizationTTL), CreatedAt: s.Now(),
	}
	if err = s.repo.CreateAuthorization(ctx, pending); err != nil {
		return nil, err
	}
	return pending, nil
}
func (s *RemoteService) Pending(ctx context.Context, id string) (*domain.AuthorizationRequest, *domain.OAuthClient, error) {
	if len(id) != 32 {
		return nil, nil, ErrInvalid
	}
	pending, err := s.repo.FindAuthorization(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if pending == nil || pending.ApprovedAt != nil || pending.RedeemedAt != nil || !s.Now().Before(pending.ExpiresAt) {
		return nil, nil, ErrOAuthGrant
	}
	client, err := s.repo.FindClient(ctx, pending.ClientID)
	if err != nil || client == nil {
		return nil, nil, ErrOAuthGrant
	}
	return pending, client, nil
}
func (s *RemoteService) Approve(ctx context.Context, id string, admin uint, selected []string) (string, error) {
	if admin == 0 {
		return "", ErrInvalid
	}
	cfg, err := s.Active(ctx)
	if err != nil {
		return "", err
	}
	pending, _, err := s.Pending(ctx, id)
	if err != nil {
		return "", err
	}
	if pending.Resource != cfg.PublicOrigin+"/mcp" {
		return "", ErrOAuthGrant
	}
	approved, err := parseScopes(selected)
	if err != nil {
		return "", err
	}
	for _, scope := range selected {
		if !HasScope(pending.Scopes, scope) {
			return "", ErrInvalid
		}
	}
	secret, err := randomHex(32)
	if err != nil {
		return "", err
	}
	code := "djcode_" + id + "_" + secret
	ok, err := s.repo.ApproveAuthorization(ctx, id, hashOpaque(code), approved, admin, s.Now())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrOAuthGrant
	}
	u, _ := url.Parse(pending.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	if pending.State != "" {
		q.Set("state", pending.State)
	}
	q.Set("iss", cfg.PublicOrigin)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Deny ends pending authorization and returns OAuth access_denied with state.
func (s *RemoteService) Deny(ctx context.Context, id string, admin uint) (string, error) {
	if admin == 0 {
		return "", ErrInvalid
	}
	cfg, err := s.Active(ctx)
	if err != nil {
		return "", err
	}
	pending, _, err := s.Pending(ctx, id)
	if err != nil {
		return "", err
	}
	if pending.Resource != cfg.PublicOrigin+"/mcp" {
		return "", ErrOAuthGrant
	}
	ok, err := s.repo.DenyAuthorization(ctx, id, s.Now())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrOAuthGrant
	}
	u, _ := url.Parse(pending.RedirectURI)
	q := u.Query()
	q.Set("error", "access_denied")
	if pending.State != "" {
		q.Set("state", pending.State)
	}
	q.Set("iss", cfg.PublicOrigin)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
