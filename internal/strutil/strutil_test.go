package strutil

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateIsRuneSafe(t *testing.T) {
	s := "مرحبا بالعالم" // نص عربي: كل حرف ≥ 2 بايت
	for n := 0; n <= 14; n++ {
		got := Truncate(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("Truncate(%d) أنتج UTF-8 غير صالح: %q", n, got)
		}
		if utf8.RuneCountInString(got) > n {
			t.Fatalf("Truncate(%d) أطول من الحد", n)
		}
	}
	if Truncate("abc", 10) != "abc" {
		t.Error("نص أقصر من الحد يجب ألا يتغير")
	}
}

func TestStringOr(t *testing.T) {
	if StringOr("x", "d") != "x" || StringOr(5, "d") != "d" || StringOr(nil, "d") != "d" {
		t.Error("StringOr")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if FirstNonEmpty("", "", "c", "d") != "c" || FirstNonEmpty("", "") != "" || FirstNonEmpty() != "" {
		t.Error("FirstNonEmpty")
	}
}
