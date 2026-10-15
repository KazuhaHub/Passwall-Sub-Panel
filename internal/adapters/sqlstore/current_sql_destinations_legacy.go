//go:build !go1.27

package sqlstore

// Older compilers use pgx's original database/sql path. Keep the declared Go
// floor and destinations unchanged; ConvertAssign is only available in Go 1.27.
func currentSQLDestinations(_ string, destinations []any) []any {
	return destinations
}
