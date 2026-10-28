package novel

import (
	"testing"
)

func TestNovelCacheFIFOAndMapReference(t *testing.T) {
	s := New(nil)
	first := map[string]any{"text": "original"}
	s.cacheSet("a", first)
	s.cacheSet("b", map[string]any{"text": "b"})

	// Reads do not promote FIFO entries, and values are returned by reference.
	got, ok := s.cacheGet("a")
	if !ok || got["text"] != "original" {
		t.Fatalf("cacheGet(a) = %#v, %v", got, ok)
	}
	got["text"] = "mutated"
	if again, ok := s.cacheGet("a"); !ok || again["text"] != "mutated" {
		t.Fatalf("cached map was not returned by reference: %#v, %v", again, ok)
	}

	// Updating A refreshes its TTL but retains its original FIFO position.
	s.cacheSet("a", map[string]any{"text": "updated"})
	for i := 0; i < cacheMaxSize-2; i++ {
		s.cacheSet(string(rune('c'+i)), map[string]any{"text": "filler"})
	}
	s.cacheSet("last", map[string]any{"text": "last"})
	if _, ok := s.cacheGet("a"); ok {
		t.Fatal("FIFO update/read should not promote a")
	}
	if _, ok := s.cacheGet("b"); !ok {
		t.Fatal("expected b to remain after FIFO evicted oldest key a")
	}
}

func TestNovelCacheClear(t *testing.T) {
	s := New(nil)
	s.cacheSet("a", map[string]any{"v": 1})
	s.cacheSet("b", map[string]any{"v": 2})
	if got := s.cacheClear(); got != 2 {
		t.Fatalf("cacheClear() = %d, want 2", got)
	}
	if _, ok := s.cacheGet("a"); ok {
		t.Fatal("entry remained after clear")
	}
	if got := s.cacheClear(); got != 0 {
		t.Fatalf("second cacheClear() = %d, want 0", got)
	}
}
