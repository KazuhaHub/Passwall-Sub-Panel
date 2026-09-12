package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sync/atomic"
)

const taskIDIssuerBytes = 24 // 192 bits, encoded as 48 lowercase hex characters.

var (
	ErrTaskIDMinterUninitialized = errors.New("task ID minter is not initialized")
	ErrTaskIDCounterExhausted    = errors.New("task ID counter is exhausted")
)

// TaskIDMinter combines a fresh random issuer with a monotonic counter. Next is
// safe for concurrent callers sharing the returned pointer. Do not copy a
// minter value, even before its first Next: copying issuer and counter can
// duplicate IDs. Each process startup (and each replica) should construct its
// own instance.
//
// The fresh issuer is a startup uniqueness measure, not a rollback detector:
// restoring or cloning VM memory can duplicate both issuer and counter, and
// rolling back both databases can lose task evidence. Those cases still need
// restore-finalize coordination; this building block does not promise business
// exactly-once execution.
type TaskIDMinter struct {
	issuer  string // immutable after successful construction
	counter atomic.Uint64
}

// NewTaskIDMinter constructs a minter with a fresh CSPRNG 192-bit issuer. It
// returns no usable minter unless the complete issuer has been read.
func NewTaskIDMinter() (*TaskIDMinter, error) {
	return newTaskIDMinter(rand.Reader)
}

// The reader seam remains private so substituting deterministic entropy is not
// part of the production API. Tests can exercise failure and partial reads
// without replacing crypto/rand.Reader process-wide.
func newTaskIDMinter(entropy io.Reader) (*TaskIDMinter, error) {
	if entropy == nil {
		return nil, errors.New("initialize task ID issuer: entropy reader is nil")
	}
	var issuer [taskIDIssuerBytes]byte
	if _, err := io.ReadFull(entropy, issuer[:]); err != nil {
		return nil, fmt.Errorf("initialize task ID issuer: %w", err)
	}
	return &TaskIDMinter{issuer: hex.EncodeToString(issuer[:])}, nil
}

// Next returns a 70-byte canonical ID:
// tsk1_<48 lowercase hex issuer>_<16 lowercase hex counter>.
// The first counter is one; the maximum uint64 value can be issued once, then
// exhaustion is permanent rather than wrapping to an earlier identity.
//
// Mint a candidate outside the database transaction. A rollback may leave a
// harmless gap; an already-stored task must retain its original identity.
// Nil receivers and zero-value minters fail closed without issuing an ID.
func (m *TaskIDMinter) Next() (string, error) {
	if m == nil || len(m.issuer) != taskIDIssuerBytes*2 {
		return "", ErrTaskIDMinterUninitialized
	}
	for {
		previous := m.counter.Load()
		if previous == math.MaxUint64 {
			return "", ErrTaskIDCounterExhausted
		}
		next := previous + 1
		if m.counter.CompareAndSwap(previous, next) {
			return fmt.Sprintf("tsk1_%s_%016x", m.issuer, next), nil
		}
	}
}
