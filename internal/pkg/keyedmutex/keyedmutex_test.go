package keyedmutex

import (
	"testing"
	"time"
)

func TestMapSerializesSameKeyAndCleansUp(t *testing.T) {
	var locks Map[string]
	unlockFirst := locks.Lock("user")

	acquired := make(chan func(), 1)
	go func() { acquired <- locks.Lock("user") }()

	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("second holder acquired the same key before it was unlocked")
	case <-time.After(20 * time.Millisecond):
	}
	if got := locks.Len(); got != 1 {
		t.Fatalf("Len while a holder and waiter exist = %d, want 1", got)
	}

	unlockFirst()
	var unlockSecond func()
	select {
	case unlockSecond = <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second holder did not acquire the key")
	}
	if got := locks.Len(); got != 1 {
		t.Fatalf("Len while second holder exists = %d, want 1", got)
	}
	unlockSecond()
	unlockSecond() // returned cleanup functions are safe to defer and call once more
	if got := locks.Len(); got != 0 {
		t.Fatalf("Len after final unlock = %d, want 0", got)
	}
}

func TestMapDoesNotSerializeDifferentKeys(t *testing.T) {
	var locks Map[int]
	unlockFirst := locks.Lock(1)
	defer unlockFirst()

	acquired := make(chan func(), 1)
	go func() { acquired <- locks.Lock(2) }()
	select {
	case unlockSecond := <-acquired:
		unlockSecond()
	case <-time.After(time.Second):
		t.Fatal("different keys unexpectedly blocked one another")
	}
}
