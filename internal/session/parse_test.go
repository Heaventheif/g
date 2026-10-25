package session

import (
	"reflect"
	"testing"
)

func TestParseMessages(t *testing.T) {
	raw := []any{
		map[string]any{"role": "user", "content": "مرحبا"},
		"not a map",
		map[string]any{"role": 5, "content": "x"},
		map[string]any{"role": "assistant"},
	}
	want := []Message{{"user", "مرحبا"}, {"", "x"}, {"assistant", ""}}
	if got := ParseMessages(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if ParseMessages("nope") != nil || ParseMessages(nil) != nil {
		t.Fatal("غير المصفوفة يجب أن يعيد nil")
	}
}
