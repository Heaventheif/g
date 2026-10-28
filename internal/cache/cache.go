// Package cache provides a concurrency-safe bounded generic cache.
package cache

import (
	"container/list"
	"sync"
	"time"
)

// Policy determines which entry is evicted when the cache reaches Capacity.
type Policy uint8

const (
	// LRU promotes entries on reads and updates.
	LRU Policy = iota
	// FIFO preserves insertion order: reads do not promote and updates retain
	// their original position.
	FIFO
)

type entry[K comparable, V any] struct {
	key     K
	value   V
	expires time.Time
}

// Cache is a concurrency-safe generic cache with a fixed capacity and an
// optional time-to-live. A non-positive TTL disables expiration. A non-positive
// capacity is treated as capacity one, so the cache always remains bounded.
type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	policy   Policy
	ttl      time.Duration
	order    *list.List
	entries  map[K]*list.Element
}

// New creates an empty cache. Unsupported policies fall back to LRU.
func New[K comparable, V any](capacity int, policy Policy, ttl time.Duration) *Cache[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	if policy != LRU && policy != FIFO {
		policy = LRU
	}
	return &Cache[K, V]{
		capacity: capacity,
		policy:   policy,
		ttl:      ttl,
		order:    list.New(),
		entries:  make(map[K]*list.Element),
	}
}

// Get returns the value for key. An expired entry is removed and is a miss.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	item := el.Value.(*entry[K, V])
	if c.expired(item, time.Now()) {
		c.remove(el)
		var zero V
		return zero, false
	}
	if c.policy == LRU {
		c.order.MoveToFront(el)
	}
	return item.value, true
}

// Set inserts or updates a value and refreshes its TTL. Updating a FIFO entry
// does not change its eviction position; updating an LRU entry makes it newest.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expires := time.Time{}
	if c.ttl > 0 {
		expires = now.Add(c.ttl)
	}
	if el, ok := c.entries[key]; ok {
		item := el.Value.(*entry[K, V])
		item.value = value
		item.expires = expires
		if c.policy == LRU {
			c.order.MoveToFront(el)
		}
		return
	}
	for len(c.entries) >= c.capacity {
		c.remove(c.order.Back())
	}
	item := &entry[K, V]{key: key, value: value, expires: expires}
	c.entries[key] = c.order.PushFront(item)
}

// Clear removes all entries and returns the number removed.
func (c *Cache[K, V]) Clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.entries)
	c.entries = make(map[K]*list.Element)
	c.order.Init()
	return n
}

// Len reports the number of stored entries, including entries not yet observed
// to have expired. Expired entries are removed on Get or by capacity eviction.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *Cache[K, V]) expired(item *entry[K, V], now time.Time) bool {
	return !item.expires.IsZero() && !now.Before(item.expires)
}

func (c *Cache[K, V]) remove(el *list.Element) {
	if el == nil {
		return
	}
	item := el.Value.(*entry[K, V])
	delete(c.entries, item.key)
	c.order.Remove(el)
}
