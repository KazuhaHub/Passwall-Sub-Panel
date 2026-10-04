package boundedcache

import (
	"fmt"
	"sync"
	"testing"
)

func TestCacheEnforcesEntryAndByteBudgetsAcrossReplacement(t *testing.T) {
	c := New[string](2, 8)
	c.Put("a", "first", 3)
	c.Put("b", "second", 3)
	c.Get("a")
	c.Put("c", "third", 2)
	if _, found := c.Get("b"); found {
		t.Fatal("recently read entry did not survive entry-limit eviction")
	}
	c.Put("a", "replacement", 7)
	if _, found := c.Get("c"); found {
		t.Fatal("replacement did not enforce byte limit")
	}
	if value, found := c.Get("a"); !found || value != "replacement" {
		t.Fatal("replacement lost")
	}
	c.Put("oversize", "never retain", 9)
	if _, found := c.Get("oversize"); found {
		t.Fatal("oversized payload retained")
	}
	c.Put("d", "last", 1)
	if c.weight != 8 || len(c.items) != 2 {
		t.Fatalf("replacement counted twice or budget incorrect: weight=%d entries=%d", c.weight, len(c.items))
	}
	var absent *Cache[string]
	absent.Put("safe", "nil", 1)
	if _, found := absent.Get("safe"); found {
		t.Fatal("nil cache retained data")
	}
}

func TestCacheConcurrentNodesStayBoundedAndCoherent(t *testing.T) {
	c := New[int](8, 64)
	var wg sync.WaitGroup
	for node := range 16 {
		wg.Go(func() {
			for round := range 100 {
				key := fmt.Sprint(node, ":", round%4)
				c.Put(key, node, 8)
				if value, found := c.Get(key); found && value != node {
					t.Errorf("cache crossed node identity: got %d, want %d", value, node)
				}
			}
		})
	}
	wg.Wait()
	if c.weight > 64 || len(c.items) > 8 || c.order.Len() != len(c.items) {
		t.Fatalf("concurrent cache exceeded budget: weight=%d entries=%d order=%d", c.weight, len(c.items), c.order.Len())
	}
}
