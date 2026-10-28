package tts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func assertChunksWithinLimit(t *testing.T, chunks []string) {
	t.Helper()
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > ttsMaxChars {
			t.Fatalf("chunk %d طوله %d runes > %d", i, n, ttsMaxChars)
		}
		if strings.TrimSpace(c) == "" {
			t.Fatalf("chunk %d فارغ", i)
		}
	}
}

func TestSplitForTTS_DelimiterAtLastAllowedIndex(t *testing.T) {
	// فاصل عند الفهرس 189 ⇒ cut = 190 == ttsMaxChars؛ يجب اعتباره فاصلاً صالحاً.
	first := strings.Repeat("a", 100) + " " + strings.Repeat("a", 88)
	text := first + " " + strings.Repeat("b", 50)
	chunks := splitForTTS(text)
	assertChunksWithinLimit(t, chunks)
	if len(chunks) != 2 || chunks[0] != first || chunks[1] != strings.Repeat("b", 50) {
		t.Fatalf("chunks = %q", chunks)
	}
}

func TestSplitForTTS_PunctuationAtIndex190NotOverLimit(t *testing.T) {
	text := strings.Repeat("a", ttsMaxChars) + "." + strings.Repeat("b", 10)
	chunks := splitForTTS(text)
	assertChunksWithinLimit(t, chunks)
	if strings.Join(chunks, "") != text {
		t.Fatalf("فُقد محتوى: %q", chunks)
	}
}

func TestSplitForTTS_NoDelimiterHardCut(t *testing.T) {
	text := strings.Repeat("x", ttsMaxChars*2+5)
	chunks := splitForTTS(text)
	assertChunksWithinLimit(t, chunks)
	if len(chunks) != 3 || strings.Join(chunks, "") != text {
		t.Fatalf("chunks = %d", len(chunks))
	}
}

func TestSplitForTTS_Arabic(t *testing.T) {
	text := strings.Repeat("مرحبا بالعالم، هذا اختبار للنص العربي الطويل. ", 20)
	chunks := splitForTTS(text)
	assertChunksWithinLimit(t, chunks)
	if len(chunks) < 2 {
		t.Fatalf("متوقع أكثر من مقطع، الناتج %d", len(chunks))
	}
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if norm(strings.Join(chunks, " ")) != norm(text) {
		t.Fatal("محتوى النص تغيّر بعد التقسيم")
	}
}

func TestSplitForTTS_ShortAndEmpty(t *testing.T) {
	if got := splitForTTS("   "); got != nil {
		t.Fatalf("got %q", got)
	}
	if got := splitForTTS("مرحبا"); len(got) != 1 || got[0] != "مرحبا" {
		t.Fatalf("got %q", got)
	}
}
