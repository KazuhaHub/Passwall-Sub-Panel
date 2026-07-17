// Package keyedmutex provides mutexes that serialize work by a comparable key.
package keyedmutex

import "sync"

// Map owns a transient mutex for every key currently in use. Entries are
// reference-counted and removed after the last holder or waiter unlocks, so a
// long-running process does not retain every key it has ever seen.
type Map[K comparable] struct {
	mu      sync.Mutex
	entries map[K]*entry
}

type entry struct {
	mu   sync.Mutex
	refs int
}

// Lock acquires the mutex associated with key and returns an idempotent unlock
// function. Different keys never block one another.
func (m *Map[K]) Lock(key K) func() {
	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[K]*entry)
	}
	e := m.entries[key]
	if e == nil {
		e = &entry{}
		m.entries[key] = e
	}
	e.refs++
	m.mu.Unlock()

	e.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Unlock()
			m.mu.Lock()
			e.refs--
			if e.refs == 0 {
				delete(m.entries, key)
			}
			m.mu.Unlock()
		})
	}
}

// Len returns the number of keys that currently have a holder or waiter. It is
// primarily useful for health checks and tests.
func (m *Map[K]) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
