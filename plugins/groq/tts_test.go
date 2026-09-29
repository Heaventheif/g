package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sunkenbot/internal/keyrotate"
)

func TestOrpheusArabicSaudiModelAndVoices(t *testing.T) {
	if got := OrpheusArabicModel(); got != "canopylabs/orpheus-arabic-saudi" {
		t.Fatalf("model = %q, want canopylabs/orpheus-arabic-saudi", got)
	}
	if got := OrpheusArabicDefaultVoice(); got != "fahad" {
		t.Fatalf("default voice = %q, want fahad", got)
	}
	want := []string{"abdullah", "fahad", "sultan", "lulwa", "noura", "aisha"}
	got := OrpheusArabicVoices()
	if len(got) != len(want) {
		t.Fatalf("voice count = %d, want %d: %v", len(got), len(want), got)
	}
	for i, voice := range want {
		if got[i] != voice {
			t.Errorf("voice[%d] = %q, want %q", i, got[i], voice)
		}
	}
}

func TestGroqTTSChunkRotatesToNextKeyAfterTransient502(t *testing.T) {
	var calls atomic.Int32
	var gotPayload map[string]any
	var usedKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		authorization := r.Header.Get("Authorization")
		usedKeys = append(usedKeys, authorization)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(body, &gotPayload); err != nil {
			t.Errorf("decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		if authorization == "Bearer first-key" {
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFF-test-wave"))
	}))
	defer server.Close()

	wantAudio := []byte("RIFF-test-wave")
	gotAudio, err := groqTTSChunkAt(
		context.Background(), server.Client(), keyrotate.New("first-key,second-key"),
		"مرحبا", "fahad", server.URL,
	)
	if err != nil {
		t.Fatalf("groqTTSChunkAt() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("request count = %d, want 2", calls.Load())
	}
	if len(usedKeys) != 2 || usedKeys[0] != "Bearer first-key" || usedKeys[1] != "Bearer second-key" {
		t.Fatalf("tried keys = %v, want first-key then second-key", usedKeys)
	}
	if !bytes.Equal(gotAudio, wantAudio) {
		t.Fatalf("audio = %q, want %q", gotAudio, wantAudio)
	}
	if gotPayload["model"] != "canopylabs/orpheus-arabic-saudi" ||
		gotPayload["voice"] != "fahad" || gotPayload["input"] != "مرحبا" ||
		gotPayload["response_format"] != "wav" {
		t.Fatalf("unexpected Groq request payload: %#v", gotPayload)
	}
}

func TestGroqTTSChunkDoesNotRetryBadRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "invalid voice", http.StatusBadRequest)
	}))
	defer server.Close()

	_, err := groqTTSChunkAt(
		context.Background(), server.Client(), keyrotate.New("test-key"),
		"مرحبا", "not-a-voice", server.URL,
	)
	if err == nil {
		t.Fatal("expected a 400 error")
	}
	if calls.Load() != 1 {
		t.Fatalf("request count = %d, want 1 for a non-retryable 400", calls.Load())
	}
	if !bytes.Contains([]byte(err.Error()), []byte("status 400")) {
		t.Fatalf("error = %q, want status 400", err)
	}
}
