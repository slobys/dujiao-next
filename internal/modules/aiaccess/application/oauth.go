package application

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

var (
	authCodePattern = regexp.MustCompile(`^djcode_([0-9a-f]{32})_([0-9a-f]{64})$`)
	refreshPattern  = regexp.MustCompile(`^djrf_([0-9a-f]{16})_([0-9a-f]{64})$`)
	verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func keyTokenForID(id string) (string, string, error) {
	raw, err := randomHex(32)
	if err != nil {
		return "", "", err
	}
	token := "djai_" + id + "_" + raw
	return token, hashOpaque(token), nil
}
func (s *RemoteService) Exchange(ctx context.Context, clientID, code, redirect, verifier, resource string) (*OAuthTokenResponse, error) {
	cfg, err := s.Active(ctx)
	if err != nil {
		return nil, ErrOAuthGrant
	}
	if resource == "" {
		resource = cfg.PublicOrigin + "/mcp"
	}
	if resource != cfg.PublicOrigin+"/mcp" {
		return nil, ErrOAuthGrant
	}
	match := authCodePattern.FindStringSubmatch(code)
	if len(match) != 3 || !verifierPattern.MatchString(verifier) {
		return nil, ErrOAuthGrant
	}
	pending, err := s.repo.FindAuthorization(ctx, match[1])
	if err != nil {
		return nil, err
	}
	if pending == nil || pending.ClientID != clientID || pending.RedirectURI != redirect ||
		pending.Resource != resource || pending.ApprovedAt == nil || pending.RedeemedAt != nil ||
		!s.Now().Before(pending.ExpiresAt) {
		return nil, ErrOAuthGrant
	}
	expected, err := hex.DecodeString(pending.CodeHash)
	if err != nil || len(expected) != sha256.Size {
		return nil, ErrOAuthGrant
	}
	received := sha256.Sum256([]byte(code))
	if subtle.ConstantTimeCompare(expected, received[:]) != 1 {
		return nil, ErrOAuthGrant
	}
	challenge := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(pending.CodeChallenge), []byte(base64.RawURLEncoding.EncodeToString(challenge[:]))) != 1 {
		return nil, ErrOAuthGrant
	}

	client, err := s.repo.FindClient(ctx, clientID)
	if err != nil || client == nil {
		return nil, ErrOAuthGrant
	}
	rawToken, id, hash, err := Generate()
	if err != nil {
		return nil, err
	}
	refreshRandom, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	rawRefresh := "djrf_" + id + "_" + refreshRandom
	now := s.Now()
	name := "OAuth: " + strings.TrimSpace(client.Name)
	if len(name) > 100 {
		name = name[:100]
	}
	key := &domain.Key{
		Name: name, KeyID: id, TokenHash: hash, Scopes: pending.Scopes,
		CreatedBy: pending.ApprovedBy,
		Audience:  resource, OAuthClientID: clientID,
		ExpiresAt: now.Add(AccessTokenTTL), CreatedAt: now, UpdatedAt: now,
	}
	refresh := &domain.OAuthRefresh{
		KeyID: id, ClientID: clientID, Audience: resource,
		TokenHash: hashOpaque(rawRefresh), ExpiresAt: now.Add(RefreshTokenTTL),
		CreatedAt: now, UpdatedAt: now,
	}
	audit := &domain.Audit{KeyID: id, ActorAdminID: pending.ApprovedBy, Action: "oauth_authorize", Result: "ok", CreatedAt: now}
	ok, err := s.repo.RedeemAuthorization(ctx, pending.ID, pending.CodeHash, now, key, refresh, audit)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrOAuthGrant
	}
	return &OAuthTokenResponse{
		AccessToken: rawToken, TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL.Seconds()),
		RefreshToken: rawRefresh, Scope: strings.ReplaceAll(pending.Scopes, ",", " "),
	}, nil
}
func (s *RemoteService) Refresh(ctx context.Context, clientID, token, resource string) (*OAuthTokenResponse, error) {
	cfg, err := s.Active(ctx)
	if err != nil {
		return nil, ErrOAuthGrant
	}
	if resource == "" {
		resource = cfg.PublicOrigin + "/mcp"
	}
	if resource != cfg.PublicOrigin+"/mcp" {
		return nil, ErrOAuthGrant
	}
	match := refreshPattern.FindStringSubmatch(token)
	if len(match) != 3 || len(clientID) > 42 {
		return nil, ErrOAuthGrant
	}
	access, hash, err := keyTokenForID(match[1])
	if err != nil {
		return nil, err
	}
	nextSecret, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	nextRefresh := "djrf_" + match[1] + "_" + nextSecret
	now := s.Now()
	ok, err := s.repo.RefreshOAuth(ctx, match[1], clientID, hashOpaque(token), resource,
		now, hash, hashOpaque(nextRefresh), now.Add(AccessTokenTTL),
		&domain.Audit{KeyID: match[1], Action: "oauth_refresh", Result: "ok", CreatedAt: now})
	if errors.Is(err, ErrNotAuthorized) {
		return nil, ErrOAuthGrant
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrOAuthGrant
	}
	// Scopes are always fetched from the still-active key; never from OAuth input.
	key, err := s.keys.AuthenticateMCP(ctx, access, resource)
	if err != nil {
		return nil, ErrOAuthGrant
	}
	return &OAuthTokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL.Seconds()),
		RefreshToken: nextRefresh, Scope: strings.ReplaceAll(key.Scopes, ",", " "),
	}, nil
}
