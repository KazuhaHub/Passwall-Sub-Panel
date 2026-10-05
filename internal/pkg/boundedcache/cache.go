// Package boundedcache holds private immutable values in a weighted LRU.
package boundedcache

import (
	"container/list"
	"sync"
)

type entry[T any] struct {
	key    string
	value  T
	weight int
}
type Cache[T any] struct {
	mu                            sync.Mutex
	items                         map[string]*list.Element
	order                         *list.List
	maxEntries, maxWeight, weight int
}

func New[T any](maxEntries, maxWeight int) *Cache[T] {
	if maxEntries <= 0 || maxWeight <= 0 {
		panic("boundedcache: positive limits required")
	}
	return &Cache[T]{items: map[string]*list.Element{}, order: list.New(), maxEntries: maxEntries, maxWeight: maxWeight}
}
func (c *Cache[T]) Get(key string) (T, bool) {
	var zero T
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, found := c.items[key]
	if !found {
		return zero, false
	}
	c.order.MoveToFront(item)
	return item.Value.(entry[T]).value, true
}
func (c *Cache[T]) Put(key string, value T, weight int) {
	if c == nil || weight <= 0 || weight > c.maxWeight {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, found := c.items[key]; found {
		c.weight -= previous.Value.(entry[T]).weight
		c.order.Remove(previous)
	}
	c.items[key] = c.order.PushFront(entry[T]{key: key, value: value, weight: weight})
	c.weight += weight
	for len(c.items) > c.maxEntries || c.weight > c.maxWeight {
		last := c.order.Back()
		old := last.Value.(entry[T])
		delete(c.items, old.key)
		c.weight -= old.weight
		c.order.Remove(last)
	}
}

func (c *Cache[T]) Delete(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if item, found := c.items[key]; found {
		c.weight -= item.Value.(entry[T]).weight
		c.order.Remove(item)
		delete(c.items, key)
	}
}

func (c *Cache[T]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = map[string]*list.Element{}
	if c.order != nil {
		c.order.Init()
	}
	c.weight = 0
}
