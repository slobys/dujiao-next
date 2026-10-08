package domain

import "time"

// RemoteConfig defaults to disabled when its singleton row does not exist.
// PublicOrigin must be a validated https://host origin with no path.
type RemoteConfig struct {
	ID           uint      `gorm:"primaryKey" json:"-"`
	Enabled      bool      `gorm:"not null;default:false" json:"enabled"`
	PublicOrigin string    `gorm:"type:varchar(253);not null;default:''" json:"public_origin"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (RemoteConfig) TableName() string { return "ai_mcp_settings" }

// OAuthClient is registered via the optional RFC 7591 compatibility endpoint.
// It is ALWAYS a public client using PKCE, never a privileged application.
type OAuthClient struct {
	ClientID     string    `gorm:"primaryKey;type:varchar(42)" json:"client_id"`
	Name         string    `gorm:"type:varchar(100);not null" json:"client_name"`
	RedirectURIs string    `gorm:"type:text;not null" json:"-"`
	CreatedAt    time.Time `gorm:"index" json:"-"`
}

func (OAuthClient) TableName() string { return "ai_mcp_oauth_clients" }

// AuthorizationRequest holds a short-lived OAuth authorization request.
// Only SHA-256 hash of authorization code is stored, never the actual code.
type AuthorizationRequest struct {
	ID            string     `gorm:"primaryKey;type:varchar(64)" json:"id"`
	ClientID      string     `gorm:"type:varchar(42);not null;index" json:"client_id"`
	RedirectURI   string     `gorm:"type:varchar(2048);not null" json:"redirect_uri"`
	Scopes        string     `gorm:"type:varchar(128);not null" json:"scopes"`
	State         string     `gorm:"type:varchar(512)" json:"-"`
	CodeChallenge string     `gorm:"type:varchar(128);not null" json:"-"`
	Resource      string     `gorm:"type:varchar(300);not null" json:"-"`
	CodeHash      string     `gorm:"type:char(64);not null;default:''" json:"-"`
	ApprovedBy    uint       `gorm:"index" json:"-"`
	ExpiresAt     time.Time  `gorm:"index;not null" json:"expires_at"`
	ApprovedAt    *time.Time `json:"-"`
	RedeemedAt    *time.Time `json:"-"`
	CreatedAt     time.Time  `gorm:"index" json:"-"`
}

func (AuthorizationRequest) TableName() string { return "ai_mcp_oauth_requests" }

// OAuthRefresh is a single-use rotating, audience-bound refresh token.
// Revoking the linked AI key invalidates this credential too.
type OAuthRefresh struct {
	KeyID     string    `gorm:"primaryKey;type:varchar(16)" json:"key_id"`
	ClientID  string    `gorm:"type:varchar(42);not null" json:"-"`
	Audience  string    `gorm:"type:varchar(300);not null" json:"-"`
	TokenHash string    `gorm:"type:char(64);not null" json:"-"`
	ExpiresAt time.Time `gorm:"index;not null" json:"-"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (OAuthRefresh) TableName() string { return "ai_mcp_oauth_refresh_tokens" }
