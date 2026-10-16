package sqlstore

import "database/sql"

// Current counter projections commonly receive int64 driver values. Handle
// those without reflection/string conversion, while preserving database/sql's
// complete conversion and failure semantics for every other representation.
type nullableInt64 struct{ sql.NullInt64 }

func (n *nullableInt64) Scan(source any) error {
	if source == nil {
		n.Int64, n.Valid = 0, false
		return nil
	}
	if value, ok := source.(int64); ok {
		n.Int64, n.Valid = value, true
		return nil
	}
	return n.NullInt64.Scan(source)
}

type nullableUint64 struct{ sql.Null[uint64] }

func (n *nullableUint64) Scan(source any) error {
	switch value := source.(type) {
	case nil:
		n.V, n.Valid = 0, false
		return nil
	case int64:
		if value >= 0 {
			n.V, n.Valid = uint64(value), true
			return nil
		}
	case uint64:
		n.V, n.Valid = value, true
		return nil
	}
	return n.Null.Scan(source)
}

var _ sql.Scanner = (*nullableInt64)(nil)
var _ sql.Scanner = (*nullableUint64)(nil)
