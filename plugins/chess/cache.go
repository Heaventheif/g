package chess

// cache.go — كاش LRU عام (يستخدمه renderer و lichessClient معاً).
// كان معرَّفاً سابقاً داخل lichess.go رغم أن renderer يعتمد عليه أيضاً،
// فانتقل هنا ليكون موقعه دالاً على عموميته داخل الحزمة.

import (
	"container/list"
	"sync"
)

type lruCache struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	m   map[string]*list.Element
}

type lruEntry struct {
	key string
	val string
}

func newLRU(max int) *lruCache {
	return &lruCache{max: max, ll: list.New(), m: make(map[string]*list.Element)}
}

func (c *lruCache) get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[k]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).val, true
	}
	return "", false
}

func (c *lruCache) put(k, v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[k]; ok {
		el.Value.(*lruEntry).val = v
		c.ll.MoveToFront(el)
		return
	}
	c.m[k] = c.ll.PushFront(&lruEntry{k, v})
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.m, last.Value.(*lruEntry).key)
	}
}
