package domain

import "time"

// OrderCancellation is a standalone, explicitly approved request. Unlike
// after-sales triage, a successful approval invokes the constrained domain
// cancellation transaction; NEVER refunds or writes payment records directly.
type OrderCancellation struct {
	ID                string     `gorm:"primaryKey;type:char(32)" json:"id"`
	KeyID             string     `gorm:"type:varchar(16);not null;index" json:"key_id"`
	OrderID           uint       `gorm:"not null;index" json:"order_id"`
	OrderNo           string     `gorm:"type:varchar(100);not null" json:"order_no"`
	Currency          string     `gorm:"type:varchar(12);not null" json:"currency"`
	ExpectedStatus    string     `gorm:"type:varchar(40);not null" json:"expected_status"`
	ExpectedTotal     string     `gorm:"type:varchar(40);not null" json:"expected_total"`
	ExpectedUpdatedAt time.Time  `gorm:"not null" json:"expected_updated_at"`
	Status            string     `gorm:"type:varchar(20);not null;index" json:"status"`
	CreatedAt         time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ExpiresAt         time.Time  `gorm:"index;not null" json:"expires_at"`
	ReviewedBy        uint       `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	FailureCode       string     `gorm:"type:varchar(40)" json:"failure_code,omitempty"`
}

func (OrderCancellation) TableName() string { return "ai_order_cancellations" }
