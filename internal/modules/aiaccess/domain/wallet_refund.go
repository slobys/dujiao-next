package domain

import "time"

// WalletRefundRequest approves a CNY wallet credit for a registered account.
// No payment-provider refund, bank transfer or bare order-status mutation.
type WalletRefundRequest struct {
	ID                string     `gorm:"primaryKey;type:char(32)" json:"id"`
	KeyID             string     `gorm:"type:varchar(16);index;not null" json:"key_id"`
	OrderID           uint       `gorm:"index;not null" json:"order_id"`
	OrderNo           string     `gorm:"type:varchar(100);not null" json:"order_no"`
	Currency          string     `gorm:"type:varchar(12);not null" json:"currency"`
	ExpectedStatus    string     `gorm:"type:varchar(40);not null" json:"expected_status"`
	ExpectedTotal     string     `gorm:"type:varchar(40);not null" json:"expected_total"`
	ExpectedRefunded  string     `gorm:"type:varchar(40);not null" json:"expected_refunded"`
	ExpectedUpdatedAt time.Time  `gorm:"not null" json:"expected_updated_at"`
	Amount            string     `gorm:"type:varchar(20);not null" json:"amount"`
	Reason            string     `gorm:"type:varchar(40);not null" json:"reason"`
	Status            string     `gorm:"type:varchar(20);index;not null" json:"status"`
	ExpiresAt         time.Time  `gorm:"index;not null" json:"expires_at"`
	CreatedAt         time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ReviewedBy        uint       `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	FailureCode       string     `gorm:"type:varchar(40)" json:"failure_code,omitempty"`
}

func (WalletRefundRequest) TableName() string { return "ai_wallet_refund_requests" }
