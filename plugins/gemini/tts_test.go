package gemini

import (
	"net/http/httptest"
	"testing"
)

func TestHandleTTSVoicesUsesGroqOnly(t *testing.T) {
	svc := &Service{}
	got, err := svc.handleTTSVoices(httptest.NewRequest("GET", "/gemini/tts/voices", nil))
	if err != nil {
		t.Fatalf("handleTTSVoices() error = %v", err)
	}
	if got["groq_model"] != "canopylabs/orpheus-arabic-saudi" {
		t.Fatalf("groq_model = %v", got["groq_model"])
	}
	if got["default_voice"] != "fahad" {
		t.Fatalf("default_voice = %v, want fahad", got["default_voice"])
	}
	voices, ok := got["groq_voices"].([]string)
	if !ok || len(voices) != 6 {
		t.Fatalf("groq_voices = %#v, want six Groq voices", got["groq_voices"])
	}
	if _, exists := got["edge_voices"]; exists {
		t.Fatal("TTS voices endpoint must not advertise Edge voices")
	}
	if _, exists := got["google_voice"]; exists {
		t.Fatal("TTS voices endpoint must not advertise Google TTS")
	}
}
