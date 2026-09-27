package gemini

import (
	"net/http"
	"testing"
)

func TestRoutesDoNotExposeSTT(t *testing.T) {
	service := New(http.DefaultClient, nil)
	for _, route := range service.Routes() {
		if route.Pattern == "/gemini/stt" {
			t.Fatal("removed Gemini STT route was registered")
		}
	}
}
