package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLRUEvictionAndUpdate(t *testing.T) {
	c := New[string, int](2, LRU, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a hit")
	}
	c.Set("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected least recently used b to be evicted")
	}
	c.Set("a", 4)
	if got, ok := c.Get("a"); !ok || got != 4 {
		t.Fatalf("updated a = %d, %v; want 4, hit", got, ok)
	}
}

func TestFIFOReadAndUpdateDoNotPromote(t *testing.T) {
	c := New[string, int](2, FIFO, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Get("a")
	c.Set("a", 3)
	c.Set("c", 4)
	if _, ok := c.Get("a"); ok {
		t.Fatal("FIFO read/update unexpectedly promoted a")
	}
	if got, ok := c.Get("b"); !ok || got != 2 {
		t.Fatalf("b = %d, %v; want hit", got, ok)
	}
}

func TestTTLExpiryAndUpdateRefresh(t *testing.T) {
	c := New[string, string](2, FIFO, 40*time.Millisecond)
	c.Set("a", "old")
	time.Sleep(25 * time.Millisecond)
	c.Set("a", "refreshed")
	time.Sleep(25 * time.Millisecond)
	if got, ok := c.Get("a"); !ok || got != "refreshed" {
		t.Fatalf("updated value expired too early: %q, %v", got, ok)
	}
	time.Sleep(25 * time.Millisecond)
	if got, ok := c.Get("a"); ok {
		t.Fatalf("expired value returned as %q", got)
	}
}

func TestClearAndLen(t *testing.T) {
	c := New[int, string](3, FIFO, 0)
	c.Set(1, "one")
	c.Set(2, "two")
	if got := c.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	if got := c.Clear(); got != 2 {
		t.Fatalf("Clear() = %d, want 2", got)
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len() after Clear = %d, want 0", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := New[int, string](64, LRU, 0)
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				key := (worker*1000 + i) % 128
				c.Set(key, fmt.Sprint(i))
				c.Get(key)
				if i%100 == 0 {
					c.Len()
				}
			}
		}(worker)
	}
	wg.Wait()
	if got := c.Len(); got > 64 {
		t.Fatalf("cache exceeded capacity: %d", got)
	}
}
