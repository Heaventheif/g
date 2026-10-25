package gemini

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
