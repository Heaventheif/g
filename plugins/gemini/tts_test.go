package gemini

import (
	"net/http/httptest"
	"testing"
)

func TestHandleTTSVoicesUsesPiperArabicOnly(t *testing.T) {
	svc := &Service{}
	got, err := svc.handleTTSVoices(httptest.NewRequest("GET", "/gemini/tts/voices", nil))
	if err != nil {
		t.Fatalf("handleTTSVoices() error = %v", err)
	}
	if got["piper_model"] != "ar_JO-kareem-medium" {
		t.Fatalf("piper_model = %v", got["piper_model"])
	}
	if got["default_voice"] != "ar_JO-kareem-medium" {
		t.Fatalf("default_voice = %v, want ar_JO-kareem-medium", got["default_voice"])
	}
	voices, ok := got["piper_voices"].([]string)
	if !ok || len(voices) != 1 || voices[0] != "ar_JO-kareem-medium" {
		t.Fatalf("piper_voices = %#v, want one Arabic Piper voice", got["piper_voices"])
	}
	if got["pipeline"] != "Piper Arabic-only local TTS" {
		t.Fatalf("pipeline = %v", got["pipeline"])
	}
	if _, exists := got["groq_voices"]; exists {
		t.Fatal("TTS voices endpoint must not advertise Groq voices")
	}
}
