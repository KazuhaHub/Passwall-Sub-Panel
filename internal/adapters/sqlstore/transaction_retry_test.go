package sqlstore

import (
	"context"
	"errors"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type testSQLStateError string

func (e testSQLStateError) Error() string    { return "test SQL state " + string(e) }
func (e testSQLStateError) SQLState() string { return string(e) }

func TestRunTransactionWithRetryReplaysOnlyServerAbortedTransactions(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		err       error
		wantTries int
	}{
		{name: "mysql_deadlock", err: &mysqldriver.MySQLError{Number: 1213}, wantTries: 3},
		{name: "serialization_failure", err: testSQLStateError("40001"), wantTries: 3},
		{name: "postgres_deadlock", err: testSQLStateError("40P01"), wantTries: 3},
		{name: "mysql_lock_timeout", err: &mysqldriver.MySQLError{Number: 1205}, wantTries: 1},
		{name: "unique_violation", err: testSQLStateError("23505"), wantTries: 1},
		{name: "ordinary_error", err: errors.New("ordinary failure"), wantTries: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			err := runTransactionWithRetry(t.Context(), db, func(*gorm.DB) error {
				attempts++
				if attempts < test.wantTries {
					return test.err
				}
				if test.wantTries == 1 {
					return test.err
				}
				return nil
			})
			if attempts != test.wantTries {
				t.Fatalf("transaction attempts = %d, want %d", attempts, test.wantTries)
			}
			if test.wantTries == 1 && !errors.Is(err, test.err) {
				t.Fatalf("non-retryable error = %v, want %v", err, test.err)
			}
			if test.wantTries > 1 && err != nil {
				t.Fatalf("retryable transaction did not recover: %v", err)
			}
		})
	}
}

func TestRunTransactionWithRetryHonorsContextDuringBackoff(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	err = runTransactionWithRetry(ctx, db, func(*gorm.DB) error {
		attempts++
		cancel()
		return testSQLStateError("40001")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("cancelled retry = attempts %d error %v", attempts, err)
	}
}
