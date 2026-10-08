package domain

import "time"

// Key is a revocable AI-only machine credential. Token material is NEVER stored.
// These keys do not represent admins and must not be accepted by JWT middleware.
type Key struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Name       string     `gorm:"type:varchar(100);not null" json:"name"`
	KeyID      string     `gorm:"type:varchar(16);uniqueIndex;not null" json:"key_id"`
	TokenHash  string     `gorm:"type:char(64);not null" json:"-"`
	Scopes     string     `gorm:"type:varchar(128);not null" json:"-"`
	CreatedBy  uint       `gorm:"not null;index" json:"created_by"`
	ExpiresAt  time.Time  `gorm:"not null;index" json:"expires_at"`
	RevokedAt  *time.Time `gorm:"index" json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (Key) TableName() string { return "ai_access_keys" }

// Audit is a content-free record of key lifecycle / API access. Neither raw
// tokens, request bodies nor response bodies are stored.
type Audit struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	KeyID        string    `gorm:"type:varchar(16);index;not null" json:"key_id"`
	ActorAdminID uint      `gorm:"index" json:"actor_admin_id"`
	Action       string    `gorm:"type:varchar(40);index;not null" json:"action"`
	Route        string    `gorm:"type:varchar(120);not null" json:"route"`
	Result       string    `gorm:"type:varchar(32);not null" json:"result"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

func (Audit) TableName() string { return "ai_access_audit_logs" }
