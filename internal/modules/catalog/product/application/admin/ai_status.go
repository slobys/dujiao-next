package productadmin

import (
	"errors"
	"time"

	productcontract "github.com/dujiao-next/internal/modules/catalog/product/contract"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/shopspring/decimal"
)

var ErrAIProductChanged = errors.New("product changed since AI approval request")

// compareAndSetStatus is an optional narrow persistence capability, used by
// explicitly approved AI changes. The UPDATE is guarded by immutable snapshot
// predicates so a concurrent human edit is never silently overwritten.
type compareAndSetStatus interface {
	CompareAndSetProductActive(string, time.Time, bool, money.Amount, bool) (bool, error)
}

func (s *AdminService) UpdateStatusIfUnchanged(
	id string,
	expectedUpdatedAt time.Time,
	expectedIsActive bool,
	expectedPrice string,
	desired bool,
) (*productdomain.Product, error) {
	if expectedUpdatedAt.IsZero() || expectedIsActive == desired {
		return nil, ErrAIProductChanged
	}
	value, err := decimal.NewFromString(expectedPrice)
	if err != nil || !value.Equal(value.Round(2)) {
		return nil, ErrAIProductChanged
	}
	original, err := s.products.GetByID(id)
	if err != nil {
		return nil, err
	}
	if original == nil {
		return nil, productcontract.ErrNotFound
	}
	if original.IsActive != expectedIsActive ||
		!original.UpdatedAt.Equal(expectedUpdatedAt) ||
		original.PriceAmount.String() != expectedPrice {
		return nil, ErrAIProductChanged
	}
	if desired {
		if err = validateActivationCategory(s.categories, original.CategoryID, productcontract.ErrProductCategoryInvalid); err != nil {
			return nil, err
		}
	}
	guarded, ok := s.products.(compareAndSetStatus)
	if !ok {
		return nil, ErrAIProductChanged
	}
	applied, err := guarded.CompareAndSetProductActive(id, expectedUpdatedAt, expectedIsActive, money.FromDecimal(value), desired)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, ErrAIProductChanged
	}
	return s.products.GetByID(id)
}
