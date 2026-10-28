package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"sunkenbot/internal/httpx"
)

// المفتاح يجب أن يُرسل بترويسة X-goog-api-key ولا يظهر في الـ URL إطلاقاً.
func TestCallGeminiSTT_KeyInHeaderNotQuery(t *testing.T) {
	var gotURL, gotHeader string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotHeader = r.Header.Get("X-goog-api-key")
		body := `{"candidates":[{"content":{"parts":[{"text":"مرحبا"}]}}]}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	s := &Service{client: client}

	got, err := s.callGeminiSTTWithKeys(context.Background(), []string{"SECRET-KEY"}, "QUJD", "audio/mp3")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got != "مرحبا" {
		t.Fatalf("transcript = %q", got)
	}
	if gotHeader != "SECRET-KEY" {
		t.Fatalf("X-goog-api-key = %q، المتوقع المفتاح", gotHeader)
	}
	if strings.Contains(gotURL, "key=") || strings.Contains(gotURL, "SECRET-KEY") {
		t.Fatalf("المفتاح ظهر في الـ URL: %s", gotURL)
	}
}

// فشل الشبكة لا يُسرّب المفتاح في رسالة الخطأ العامة.
func TestCallGeminiSTT_NetworkErrorDoesNotLeakKey(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	s := &Service{client: client}
	_, err := s.callGeminiSTTWithKeys(context.Background(), []string{"SECRET-KEY"}, "QUJD", "audio/mp3")
	if err == nil {
		t.Fatal("متوقع خطأ")
	}
	if strings.Contains(err.Error(), "SECRET-KEY") {
		t.Fatalf("الخطأ يسرّب المفتاح: %v", err)
	}
}

func TestHandleGeminiSTTRejectsUnpaddedInputBeforeProvider(t *testing.T) {
	s := &Service{}
	_, err := s.handleSTT(context.Background(), STTRequest{AudioBase64: "YQ"})
	var httpErr *httpx.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("error = %v, want HTTP 400", err)
	}
}

func TestHandleGeminiSTTRejectsEmptyDecodedAudio(t *testing.T) {
	s := &Service{}
	_, err := s.handleSTT(context.Background(), STTRequest{AudioBase64: "data:audio/wav;name=empty,"})
	var httpErr *httpx.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("error = %v, want HTTP 400 for empty decoded audio", err)
	}
}

func TestHandleGeminiSTTOversizedInput(t *testing.T) {
	encoded := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxSTTAudioBytes+1))
	s := &Service{}
	_, err := s.handleSTT(context.Background(), STTRequest{AudioBase64: encoded})
	var httpErr *httpx.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("error = %v, want HTTP 413", err)
	}
}
