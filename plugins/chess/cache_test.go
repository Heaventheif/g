package chess

import "testing"

func TestLRUCacheHitEvictionAndEmptyValue(t *testing.T) {
	c := newLRU(2)
	c.put("empty", "")
	if value, ok := c.get("empty"); !ok || value != "" {
		t.Fatalf("empty value = %q, %v; want a cache hit", value, ok)
	}
	c.put("b", "B")
	if _, ok := c.get("empty"); !ok {
		t.Fatal("reading an entry should retain it and promote it")
	}
	c.put("c", "C")
	if _, ok := c.get("b"); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if value, ok := c.get("c"); !ok || value != "C" {
		t.Fatalf("new entry = %q, %v; want C, hit", value, ok)
	}
}

func TestLRUCacheUpdatePromotes(t *testing.T) {
	c := newLRU(2)
	c.put("a", "old")
	c.put("b", "B")
	c.put("a", "new")
	c.put("c", "C")
	if value, ok := c.get("a"); !ok || value != "new" {
		t.Fatalf("updated entry = %q, %v; want new, hit", value, ok)
	}
	if _, ok := c.get("b"); ok {
		t.Fatal("update should promote key a, evicting b")
	}
}
