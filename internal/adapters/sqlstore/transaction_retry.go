package sqlstore

import (
	"context"
	"errors"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

const maxTransactionAttempts = 3

type sqlStateError interface {
	SQLState() string
}

// runTransactionWithRetry retries the complete callback only for database
// errors which explicitly mean that the server aborted the transaction and
// requires a replay. The short context-aware backoff gives the winning
// transaction time to commit. Lock-wait timeouts and arbitrary I/O failures
// are deliberately not retried here because their commit state and remedy are
// different.
func runTransactionWithRetry(ctx context.Context, db *gorm.DB, transaction func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < maxTransactionAttempts; attempt++ {
		err = db.WithContext(ctx).Transaction(transaction)
		if err == nil || !retryableTransactionError(err) || attempt == maxTransactionAttempts-1 {
			return err
		}
		backoff := time.Duration(1<<attempt) * 5 * time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func retryableTransactionError(err error) bool {
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) {
		// ER_LOCK_DEADLOCK. Do not include ER_LOCK_WAIT_TIMEOUT (1205): only
		// a server-declared deadlock victim is guaranteed to have its complete
		// transaction rolled back under every supported MySQL configuration.
		return mysqlError.Number == 1213
	}
	var stateError sqlStateError
	if !errors.As(err, &stateError) {
		return false
	}
	switch stateError.SQLState() {
	case "40001", "40P01": // serialization failure, PostgreSQL deadlock
		return true
	default:
		return false
	}
}
