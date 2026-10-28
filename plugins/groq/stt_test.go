package groq

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"sunkenbot/internal/httpx"
)

func TestHandleSTTRejectsEmptyAndInvalidBase64BeforeProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{name: "empty", body: map[string]any{"base64": ""}},
		{name: "malformed", body: map[string]any{"base64": "!!!!"}},
		{name: "unpadded", body: map[string]any{"base64": "YQ"}},
		{name: "data URI not accepted", body: map[string]any{"base64": "data:audio/wav;base64,QUJD"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{}
			_, err := service.handleSTT(t.Context(), tc.body)
			var httpErr *httpx.HTTPError
			if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
				t.Fatalf("error = %v, want HTTP 400 before provider", err)
			}
		})
	}
}

func TestHandleSTTRejectsOversizedDecodedAudio(t *testing.T) {
	encoded := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxSTTBytes+1))
	service := &Service{}
	_, err := service.handleSTT(t.Context(), map[string]any{"base64": encoded})
	var httpErr *httpx.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("error = %v, want HTTP 400 for audio over 25MB", err)
	}
}
