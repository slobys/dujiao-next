package domain

import "time"

const (
	ActionProductStatus = "product_status"
	ActionPending       = "pending"
	ActionExecuting     = "executing"
	ActionSucceeded     = "succeeded"
	ActionRejected      = "rejected"
	ActionConflict      = "conflict"
	ActionFailed        = "failed"
)

// ActionRequest persists a single proposed product status change. AI tokens
// can only submit; only a system_admin can claim and execute after approval.
// An executing action is never automatically retried on process restart.
type ActionRequest struct {
	ID                string     `gorm:"primaryKey;type:char(32)" json:"id"`
	KeyID             string     `gorm:"type:varchar(16);index;not null" json:"key_id"`
	Action            string     `gorm:"type:varchar(40);not null" json:"action"`
	ProductID         uint       `gorm:"index;not null" json:"product_id"`
	ProductTitle      string     `gorm:"type:varchar(240);not null" json:"product_title"`
	ExpectedActive    bool       `gorm:"not null" json:"expected_active"`
	DesiredActive     bool       `gorm:"not null" json:"desired_active"`
	ExpectedPrice     string     `gorm:"type:varchar(40);not null" json:"expected_price"`
	ExpectedUpdatedAt time.Time  `gorm:"not null" json:"expected_updated_at"`
	Status            string     `gorm:"type:varchar(20);index;not null" json:"status"`
	ExpiresAt         time.Time  `gorm:"index;not null" json:"expires_at"`
	CreatedAt         time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ReviewedBy        uint       `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	FailureCode       string     `gorm:"type:varchar(40)" json:"failure_code,omitempty"`
}

func (ActionRequest) TableName() string { return "ai_action_requests" }
