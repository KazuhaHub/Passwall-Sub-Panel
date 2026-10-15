package sqlstore

import (
	"database/sql"
	"errors"
)

// requiredInt64 preserves *int64 scan semantics, including rejecting SQL NULL.
// The value receiver lets a reusable destination implement sql.Scanner without
// another pointer indirection during pgx's per-column destination classification.
type requiredInt64 struct{ value *int64 }

var _ sql.Scanner = requiredInt64{}

func (s requiredInt64) Scan(source any) error {
	if source == nil {
		return errors.New("converting NULL to int64 is unsupported")
	}
	if value, ok := source.(int64); ok {
		*s.value = value
		return nil
	}
	var value sql.NullInt64
	if err := value.Scan(source); err != nil {
		return err
	}
	*s.value = value.Int64
	return nil
}
