package mangabridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateJobRoute_RejectsOversizedBody(t *testing.T) {
	body := `{"manga":"` + strings.Repeat("a", maxCreateJobBodyBytes*2) + `","chapter":"1"}`
	req := httptest.NewRequest("POST", "/manga-bridge/jobs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	New().Routes()[0].Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"error":"request body too large"`) {
		t.Fatalf("status/body = %d %s, want normalized 413", rec.Code, rec.Body.String())
	}
}

func TestCreateJobRoute_InvalidJSONIsNormalized400(t *testing.T) {
	req := httptest.NewRequest("POST", "/manga-bridge/jobs", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	New().Routes()[0].Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error":"invalid request body"`) {
		t.Fatalf("status/body = %d %s, want normalized 400", rec.Code, rec.Body.String())
	}
}
