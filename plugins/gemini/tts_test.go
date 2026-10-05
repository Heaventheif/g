package gemini

import (
	"net/http/httptest"
	"testing"
)

func TestHandleTTSVoicesUsesGroqPrimaryAndPiperFallback(t *testing.T) {
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
	groqVoices, ok := got["groq_voices"].([]string)
	if !ok || len(groqVoices) != 6 {
		t.Fatalf("groq_voices = %#v, want six Groq voices", got["groq_voices"])
	}
	piperVoices, ok := got["piper_voices"].([]string)
	if !ok || len(piperVoices) != 1 || piperVoices[0] != "ar_JO-kareem-medium" {
		t.Fatalf("piper_voices = %#v, want one Piper fallback voice", got["piper_voices"])
	}
	if got["pipeline"] != "Groq Orpheus Arabic Saudi primary; Piper Arabic fallback" {
		t.Fatalf("pipeline = %v", got["pipeline"])
	}
}
