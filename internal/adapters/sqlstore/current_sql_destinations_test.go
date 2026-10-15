package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestCurrentSQLDestinationsPreserveDriverConversionsAndFailures(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	db, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		name string
		new  func() any
	}{
		{"integer", func() any { value := int64(71); return &value }},
		{"nullable-integer", func() any { return new(*int64) }},
		{"string", func() any { value := "unchanged"; return &value }},
		{"boolean", func() any { value := true; return &value }},
		{"nullable-boolean", func() any { return new(*bool) }},
		{"bytes", func() any { value := []byte("unchanged"); return &value }},
		{"scanner", func() any { return &sql.NullInt64{Int64: 71, Valid: true} }},
	} {
		for _, expression := range []string{"NULL", "0", "-17", "'9007199254740993'", "'9223372036854775808'", "'invalid-integer'", "'1.5'", "'true'"} {
			t.Run(target.name+"/"+expression, func(t *testing.T) {
				plain, adapted := target.new(), target.new()
				plainErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(plain)
				// Exercise the PostgreSQL conversion adapter against the selected
				// real dialect, including its own native driver representations.
				destinations := currentSQLDestinations("postgres", []any{adapted})
				adaptedErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(destinations...)
				if fmt.Sprint(plainErr) != fmt.Sprint(adaptedErr) || !reflect.DeepEqual(plain, adapted) {
					t.Fatal("adapted scalar changed driver conversion, NULL, error or destination value")
				}
			})
		}
	}
}

type currentSQLFailureScanner struct {
	calls int
	err   error
}

func (s *currentSQLFailureScanner) Scan(any) error {
	s.calls++
	return s.err
}

func TestCurrentSQLDestinationsDelegateScannersOnceAndLeaveOtherDialectsUnchanged(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	db, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("controlled scanner failure")
	scanner := &currentSQLFailureScanner{err: want}
	values := currentSQLDestinations("postgres", []any{scanner})
	if values[0] != scanner {
		t.Fatal("an existing SQL scanner was wrapped or replaced")
	}
	if err := db.QueryRowContext(t.Context(), "SELECT 1").Scan(values...); !errors.Is(err, want) || scanner.calls != 1 {
		t.Fatal("scanner failure was hidden, retried or converted twice")
	}
	var number int64
	for _, dialect := range []string{"sqlite", "mysql"} {
		values := []any{&number, scanner}
		got := currentSQLDestinations(dialect, values)
		if len(got) != 2 || &got[0] != &values[0] || got[0] != &number || got[1] != scanner {
			t.Fatal("non-PostgreSQL destinations were changed")
		}
	}
}
