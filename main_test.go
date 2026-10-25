package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sunkenbot/internal/plugins"
)

type fakeSvc struct{}

func (fakeSvc) Name() string { return "fake" }
func (fakeSvc) Routes() []plugins.Route {
	return []plugins.Route{{Method: "GET", Pattern: "/fake", Handler: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}}}
}

func do(h http.Handler, method, path, token string) int {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestNewHandlerRoutingAndAuth(t *testing.T) {
	h := newHandler([]plugins.Service{fakeSvc{}}, "secret")
	if c := do(h, "GET", "/health", ""); c != 200 {
		t.Errorf("/health بلا توكن: %d", c)
	}
	if c := do(h, "GET", "/", ""); c != 200 {
		t.Errorf("/ بلا توكن: %d", c)
	}
	if c := do(h, "GET", "/fake", ""); c != 401 {
		t.Errorf("مسار محمي بلا توكن يجب 401، حصل %d", c)
	}
	if c := do(h, "GET", "/fake", "secret"); c != 204 {
		t.Errorf("مسار محمي بتوكن صحيح يجب 204، حصل %d", c)
	}
}

func TestListenPort(t *testing.T) {
	t.Setenv("PORT", "")
	if listenPort() != defaultPort {
		t.Error("PORT فارغ")
	}
	t.Setenv("PORT", "abc")
	if listenPort() != defaultPort {
		t.Error("PORT غير رقمي")
	}
	t.Setenv("PORT", "9000")
	if listenPort() != "9000" {
		t.Error("PORT صحيح")
	}
}

func TestCheckDescriptions(t *testing.T) {
	svcs := []plugins.Service{fakeSvc{}}
	if err := checkDescriptions(svcs, map[string]string{}); err == nil {
		t.Error("خدمة بلا وصف يجب أن تفشل")
	}
	if err := checkDescriptions(svcs, map[string]string{"fake": "وصف"}); err != nil {
		t.Errorf("خدمة بوصف: %v", err)
	}
}

func TestCheckAuthConfig(t *testing.T) {
	if err := checkAuthConfig("1", ""); err == nil {
		t.Error("REQUIRE_AUTH=1 بلا توكن يجب أن يفشل")
	}
	if err := checkAuthConfig("1", "  "); err == nil {
		t.Error("توكن مسافات فقط يجب أن يفشل")
	}
	if err := checkAuthConfig("1", "tok"); err != nil {
		t.Errorf("REQUIRE_AUTH=1 مع توكن: %v", err)
	}
	if err := checkAuthConfig("", ""); err != nil {
		t.Errorf("بلا REQUIRE_AUTH السلوك القديم: %v", err)
	}
}
