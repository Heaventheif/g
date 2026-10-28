package chess

import (
	"sunkenbot/internal/cache"
)

// lruCache preserves the chess package's string-cache API while delegating
// storage, locking, and LRU eviction to the shared generic cache.
type lruCache struct {
	store *cache.Cache[string, string]
}

func newLRU(max int) *lruCache {
	return &lruCache{store: cache.New[string, string](max, cache.LRU, 0)}
}

func (c *lruCache) get(key string) (string, bool) {
	return c.store.Get(key)
}

func (c *lruCache) put(key, value string) {
	c.store.Set(key, value)
}
