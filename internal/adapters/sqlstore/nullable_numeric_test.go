package sqlstore

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestNullableNumericPreservesStandardSQLConversionsAndReuse(t *testing.T) {
	values := []any{nil, int64(0), int64(17), int64(-17), int64(math.MaxInt64), int64(math.MinInt64),
		uint64(17), uint64(math.MaxUint64), "0", "-1", "9007199254740993", "18446744073709551615", "18446744073709551616",
		[]byte("9223372036854775807"), []byte("18446744073709551615"), []byte(nil), "", "invalid-integer", "1.5",
		float64(17), float64(-17), float64(1.5), true, time.Unix(0, 0).UTC(), nil, int64(31), nil}
	var signed nullableInt64
	var unsigned nullableUint64
	var plainSigned sql.NullInt64
	var plainUnsigned sql.Null[uint64]
	for i, value := range values {
		t.Run(fmt.Sprintf("%02d_%T", i, value), func(t *testing.T) {
			wantSignedErr, gotSignedErr := plainSigned.Scan(value), signed.Scan(value)
			if fmt.Sprint(wantSignedErr) != fmt.Sprint(gotSignedErr) || !reflect.DeepEqual(plainSigned, signed.NullInt64) {
				t.Fatal("signed driver conversion, failure state or NULL reset differs from database/sql")
			}
			wantUnsignedErr, gotUnsignedErr := plainUnsigned.Scan(value), unsigned.Scan(value)
			if fmt.Sprint(wantUnsignedErr) != fmt.Sprint(gotUnsignedErr) || !reflect.DeepEqual(plainUnsigned, unsigned.Null) {
				t.Fatal("unsigned driver conversion, failure state or NULL reset differs from database/sql")
			}
		})
	}
}

func TestNullableNumericMatchesActualDriverScans(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	db, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{"NULL", "0", "-17", "9223372036854775807", "-9223372036854775808", "'9007199254740993'", "'18446744073709551615'", "'18446744073709551616'", "'invalid-integer'", "'1.5'"} {
		t.Run(expression, func(t *testing.T) {
			var signed nullableInt64
			var unsigned nullableUint64
			var plainSigned sql.NullInt64
			var plainUnsigned sql.Null[uint64]
			wantSignedErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(&plainSigned)
			gotSignedErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(&signed)
			if (expression == "NULL" || expression == "0") && (wantSignedErr != nil || gotSignedErr != nil) {
				t.Fatal("real database did not execute a valid signed numeric query")
			}
			if fmt.Sprint(wantSignedErr) != fmt.Sprint(gotSignedErr) || !reflect.DeepEqual(plainSigned, signed.NullInt64) {
				t.Fatal("signed real-driver scan differs from database/sql")
			}
			wantUnsignedErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(&plainUnsigned)
			gotUnsignedErr := db.QueryRowContext(t.Context(), "SELECT "+expression).Scan(&unsigned)
			if (expression == "NULL" || expression == "0") && (wantUnsignedErr != nil || gotUnsignedErr != nil) {
				t.Fatal("real database did not execute a valid unsigned numeric query")
			}
			if fmt.Sprint(wantUnsignedErr) != fmt.Sprint(gotUnsignedErr) || !reflect.DeepEqual(plainUnsigned, unsigned.Null) {
				t.Fatal("unsigned real-driver scan differs from database/sql")
			}
		})
	}
}

// This isolates numeric conversion only. It does not measure database I/O,
// Node/core processes or actual global pressure and cannot satisfy those gates.
func BenchmarkFleetNullableNumericConversion(b *testing.B) {
	for _, optimized := range []bool{false, true} {
		name := "database_sql"
		if optimized {
			name = "direct_native_values"
		}
		b.Run(name, func(b *testing.B) {
			var up, down nullableInt64
			var epoch nullableUint64
			var plainUp, plainDown sql.NullInt64
			var plainEpoch sql.Null[uint64]
			destinations := []sql.Scanner{&plainUp, &plainDown, &plainEpoch}
			if optimized {
				destinations = []sql.Scanner{&up, &down, &epoch}
			}
			values := []any{int64(1234567), int64(7654321), int64(31)}
			b.ReportAllocs()
			for b.Loop() {
				for i, destination := range destinations {
					if err := destination.Scan(values[i]); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
