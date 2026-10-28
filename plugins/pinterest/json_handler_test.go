package pinterest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPinterestRoute_InvalidJSONUsesSharedBodyError(t *testing.T) {
	svc := New(http.DefaultClient)
	req := httptest.NewRequest(http.MethodPost, "/pinterest", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	svc.Routes()[0].Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error":"invalid request body"`) {
		t.Fatalf("status/body = %d %s, want normalized 400", rec.Code, rec.Body.String())
	}
}
