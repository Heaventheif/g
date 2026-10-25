package mangabridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sunkenbot/internal/httpx"
)

func TestHandleCreateJob_RejectsOversizedBody(t *testing.T) {
	body := `{"manga":"` + strings.Repeat("a", maxCreateJobBodyBytes*2) + `","chapter":"1"}`
	req := httptest.NewRequest("POST", "/manga-bridge/jobs", strings.NewReader(body))
	_, err := New().handleCreateJob(req)
	he, ok := err.(*httpx.HTTPError)
	if !ok || he.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("err = %#v، المتوقع HTTPError 413", err)
	}
}

func TestHandleCreateJob_InvalidJSONIs400(t *testing.T) {
	req := httptest.NewRequest("POST", "/manga-bridge/jobs", strings.NewReader("{bad"))
	_, err := New().handleCreateJob(req)
	he, ok := err.(*httpx.HTTPError)
	if !ok || he.Code != http.StatusBadRequest {
		t.Fatalf("err = %#v، المتوقع HTTPError 400", err)
	}
}
