package domain

import "time"

const (
	OrderReviewPending  = "pending"
	OrderReviewAccepted = "accepted"
	OrderReviewRejected = "rejected"
	OrderReviewResolved = "resolved"
	OrderReviewConflict = "conflict"
)

// OrderReview is an internal after-sales triage ticket, NOT a refund,
// cancellation, delivery, payment, or order status mutation.
type OrderReview struct {
	ID                string     `gorm:"primaryKey;type:char(32)" json:"id"`
	KeyID             string     `gorm:"type:varchar(16);not null;index" json:"key_id"`
	OrderID           uint       `gorm:"not null;index" json:"order_id"`
	OrderNo           string     `gorm:"type:varchar(100);not null" json:"order_no"`
	Reason            string     `gorm:"type:varchar(40);not null" json:"reason"`
	ExpectedStatus    string     `gorm:"type:varchar(40);not null" json:"expected_status"`
	ExpectedTotal     string     `gorm:"type:varchar(40);not null" json:"expected_total"`
	Currency          string     `gorm:"type:varchar(12);not null" json:"currency"`
	ExpectedUpdatedAt time.Time  `gorm:"not null" json:"expected_updated_at"`
	Status            string     `gorm:"type:varchar(20);not null;index" json:"status"`
	CreatedAt         time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ExpiresAt         time.Time  `gorm:"index;not null" json:"expires_at"`
	ReviewedBy        uint       `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	ResolvedAt        *time.Time `json:"resolved_at"`
}

func (OrderReview) TableName() string { return "ai_order_reviews" }
