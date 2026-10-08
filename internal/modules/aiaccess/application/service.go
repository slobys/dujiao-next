package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dujiao-next/internal/modules/aiaccess/contract"
	"github.com/dujiao-next/internal/modules/aiaccess/domain"
)

const (
	ScopeCatalog            = "catalog:read"
	ScopeInventory          = "inventory:read"
	ScopeReport             = "report:read"
	ScopeDraftWrite         = "catalog:draft:write"
	ScopePublishRequest     = "catalog:publish:request"
	ScopeOrdersRead         = "orders:read"
	ScopeOrderReviewRequest = "orders:review:request"
)

var (
	ErrInvalid       = errors.New("invalid AI credential request")
	ErrNotFound      = errors.New("AI credential not found")
	ErrNotAuthorized = errors.New("AI credential invalid, revoked, expired or missing scope")
	keyPattern       = regexp.MustCompile(`^djai_([0-9a-f]{16})_([0-9a-f]{64})$`)
	permitted        = map[string]bool{ScopeCatalog: true, ScopeInventory: true, ScopeReport: true, ScopeDraftWrite: true, ScopePublishRequest: true, ScopeOrdersRead: true, ScopeOrderReviewRequest: true}
)

type Service struct {
	repo contract.Repository
	now  func() time.Time
}

func New(repo contract.Repository) *Service { return &Service{repo: repo, now: time.Now} }
func (s *Service) Now() time.Time           { return s.now().UTC() }

func validCredentialName(name string) bool {
	if len(name) < 1 || len(name) > 100 || utf8.RuneCountInString(name) > 100 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func parseScopes(raw []string) (string, error) {
	if len(raw) < 1 || len(raw) > len(permitted) {
		return "", ErrInvalid
	}
	unique := map[string]bool{}
	for _, scope := range raw {
		if !permitted[scope] || unique[scope] {
			return "", ErrInvalid
		}
		unique[scope] = true
	}
	items := make([]string, 0, len(unique))
	for k := range unique {
		items = append(items, k)
	}
	sort.Strings(items)
	return strings.Join(items, ","), nil
}
func HasScope(scopes, requested string) bool {
	for _, v := range strings.Split(scopes, ",") {
		if v == requested {
			return true
		}
	}
	return false
}
func Generate() (token, id, hash string, err error) {
	b := make([]byte, 8+32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	id = hex.EncodeToString(b[:8])
	token = "djai_" + id + "_" + hex.EncodeToString(b[8:])
	h := sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(h[:])
	return
}
func (s *Service) Create(ctx context.Context, name string, scopes []string, days int, adminID uint) (*domain.Key, string, error) {
	name = strings.TrimSpace(name)
	if !validCredentialName(name) || days < 1 || days > 90 || adminID == 0 {
		return nil, "", ErrInvalid
	}
	joined, err := parseScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	token, id, hash, err := Generate()
	if err != nil {
		return nil, "", err
	}
	now := s.Now()
	key := &domain.Key{Name: name, KeyID: id, TokenHash: hash, Scopes: joined, CreatedBy: adminID, ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour)}
	if err = s.repo.CreateWithAudit(ctx, key, &domain.Audit{KeyID: id, ActorAdminID: adminID, Action: "create", Result: "ok", CreatedAt: now}); err != nil {
		return nil, "", err
	}
	return key, token, nil
}
func (s *Service) Rotate(ctx context.Context, id uint, adminID uint) (string, error) {
	if id == 0 || adminID == 0 {
		return "", ErrInvalid
	}
	prev, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", err
	}
	if prev == nil || prev.RevokedAt != nil || !s.Now().Before(prev.ExpiresAt) {
		return "", ErrNotFound
	}
	// Rotation changes only the hash, immediately invalidating the old token.
	token, keyID, hash, err := Generate()
	if err != nil {
		return "", err
	}
	_ = keyID // the public ID is stable across rotation; store only a new secret.
	secretPart := strings.Split(token, "_")[2]
	resultToken := "djai_" + prev.KeyID + "_" + secretPart
	digest := sha256.Sum256([]byte(resultToken))
	hash = hex.EncodeToString(digest[:])
	now := s.Now()
	ok, err := s.repo.RotateWithAudit(ctx, id, hash, now, &domain.Audit{KeyID: prev.KeyID, ActorAdminID: adminID, Action: "rotate", Result: "ok", CreatedAt: now})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	return resultToken, nil
}
func (s *Service) Revoke(ctx context.Context, id uint, adminID uint) error {
	if id == 0 || adminID == 0 {
		return ErrInvalid
	}
	prev, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if prev == nil {
		return ErrNotFound
	}
	now := s.Now()
	ok, err := s.repo.RevokeWithAudit(ctx, id, now, &domain.Audit{KeyID: prev.KeyID, ActorAdminID: adminID, Action: "revoke", Result: "ok", CreatedAt: now})
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
func (s *Service) List(ctx context.Context) ([]domain.Key, error) { return s.repo.List(ctx) }
func (s *Service) Audits(ctx context.Context, limit int) ([]domain.Audit, error) {
	if limit <= 0 || limit > 200 {
		return nil, ErrInvalid
	}
	return s.repo.Audits(ctx, limit)
}

// Authenticate limits old /api/v1/ai/* APIs to original non-OAuth keys.
// Audience-bound OAuth tokens are valid ONLY for their MCP resource.
func (s *Service) Authenticate(ctx context.Context, token, scope string) (*domain.Key, error) {
	if !permitted[scope] {
		return nil, ErrNotAuthorized
	}
	key, err := s.authenticateRaw(ctx, token)
	if err != nil {
		return nil, err
	}
	if key.Audience != "" || !HasScope(key.Scopes, scope) {
		return nil, ErrNotAuthorized
	}
	return key, nil
}
func (s *Service) AuthenticateMCP(ctx context.Context, token, resource string) (*domain.Key, error) {
	key, err := s.authenticateRaw(ctx, token)
	if err != nil {
		return nil, err
	}
	if resource == "" || key.Audience != resource {
		return nil, ErrNotAuthorized
	}
	return key, nil
}
func (s *Service) authenticateRaw(ctx context.Context, token string) (*domain.Key, error) {
	parts := keyPattern.FindStringSubmatch(token)
	if parts == nil {
		return nil, ErrNotAuthorized
	}
	key, err := s.repo.FindKey(ctx, parts[1])
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, ErrNotAuthorized
	}
	digest := sha256.Sum256([]byte(token))
	stored, err := hex.DecodeString(key.TokenHash)
	if err != nil || len(stored) != 32 {
		return nil, ErrNotAuthorized
	}
	if subtle.ConstantTimeCompare(stored, digest[:]) != 1 || key.RevokedAt != nil || !s.Now().Before(key.ExpiresAt) {
		return nil, ErrNotAuthorized
	}
	return key, nil
}

func (s *Service) RecordUse(ctx context.Context, key *domain.Key, route, result string) error {
	if key == nil {
		return fmt.Errorf("nil ai key")
	}
	if err := s.repo.Touch(ctx, key.ID, s.Now()); err != nil {
		return err
	}
	return s.repo.Audit(ctx, &domain.Audit{KeyID: key.KeyID, Action: "access", Route: route, Result: result, CreatedAt: s.Now()})
}
