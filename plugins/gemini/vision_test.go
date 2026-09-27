package gemini

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCallGeminiVisionRetries503ThenUsesFreeFallback(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_API_KEY_2", "")
	t.Setenv("GEMINI_API_KEY_3", "")
	t.Setenv("GEMINI_API_KEY_4", "")

	requestCount := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		if strings.Contains(req.URL.Path, "/models/gemini-3.6-flash:") && requestCount <= maxGeminiVisionAttempts {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"overloaded"}}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		if !strings.Contains(req.URL.Path, "/models/gemini-3.8-flash:") {
			t.Fatalf("expected free fallback model, got URL %s", req.URL)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if strings.Contains(string(body), "google_search") {
			t.Fatal("free-tier vision request must not enable paid-only Search Grounding")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"تم تحليل الصورة"}]}}]}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	service := New(client, nil)
	reply, _, err := service.callGeminiVision(context.Background(), []byte("image"), "image/jpeg", "صف الصورة")
	if err != nil {
		t.Fatalf("callGeminiVision returned error: %v", err)
	}
	if reply != "تم تحليل الصورة" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if requestCount != maxGeminiVisionAttempts+1 {
		t.Fatalf("expected %d API calls, got %d", maxGeminiVisionAttempts+1, requestCount)
	}
}
