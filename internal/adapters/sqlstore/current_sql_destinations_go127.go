//go:build go1.27

package sqlstore

import (
	"database/sql"
	"database/sql/driver"
)

// These destinations are ordinary scalar fields, nullable scalar pointers and
// SQL scanners, never RawBytes or cursor-valued columns. Forward to the same
// database/sql conversion pgx would choose, without repeatedly classifying
// every pointer level for every cell. The wrapper has a value receiver so pgx
// recognizes sql.Scanner before walking any pointer or native-array interfaces.
type currentSQLScalarDestination struct{ destination any }

var _ sql.Scanner = currentSQLScalarDestination{}

func (d currentSQLScalarDestination) Scan(source any) error {
	return sql.ConvertAssign(driver.ScanContext{}, d.destination, source)
}

func currentSQLDestinations(dialect string, destinations []any) []any {
	if dialect != "postgres" {
		return destinations
	}
	for i, destination := range destinations {
		if _, scanner := destination.(sql.Scanner); !scanner {
			destinations[i] = currentSQLScalarDestination{destination}
		}
	}
	return destinations
}
