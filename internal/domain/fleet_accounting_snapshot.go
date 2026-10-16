package domain

// FleetAccountingSnapshot owns a complete current accounting read. Its rows and
// ID index are private and immutable, so concurrent consumers can share one SQL
// batch without copying the fleet or rebuilding its index for each response.
// It does not retain results across repository calls or contain live counters.
type FleetAccountingSnapshot struct {
	rows    []PSPClientAccounting
	byID    map[int64]int
	invalid bool
}

// NewFleetAccountingSnapshot copies every row, in order, including unusual IDs.
// Duplicate or nonpositive IDs disable indexed accounting, preserving the
// independent last-row-wins fallback used by custom repositories.
func NewFleetAccountingSnapshot(rows []PSPClientAccounting) FleetAccountingSnapshot {
	var snapshot FleetAccountingSnapshot
	if rows == nil {
		return snapshot
	}
	snapshot.rows = make([]PSPClientAccounting, len(rows))
	copy(snapshot.rows, rows)
	snapshot.byID = make(map[int64]int, len(rows))
	for i, row := range snapshot.rows {
		if _, duplicate := snapshot.byID[row.ID]; duplicate || row.ID <= 0 {
			snapshot.invalid = true
			snapshot.byID = nil
			break
		}
		snapshot.byID[row.ID] = i
	}
	return snapshot
}

func (s FleetAccountingSnapshot) Len() int { return len(s.rows) }

// Row returns all six numeric fields by value, never a mutable row reference.
func (s FleetAccountingSnapshot) Row(i int) PSPClientAccounting { return s.rows[i] }

func (s FleetAccountingSnapshot) HasUniqueIDs() bool { return !s.invalid }

func (s FleetAccountingSnapshot) IndexForID(id int64) (int, bool) {
	i, ok := s.byID[id]
	return i, ok
}

// CloneRows preserves nil versus empty and gives mutable callers an owned slice
// with capacity equal to its complete length.
func (s FleetAccountingSnapshot) CloneRows() []PSPClientAccounting {
	if s.rows == nil {
		return nil
	}
	rows := make([]PSPClientAccounting, len(s.rows))
	copy(rows, s.rows)
	return rows
}
