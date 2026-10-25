package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsProbe(t *testing.T) {
	cases := []struct {
		remote string
		path   string
		want   bool
	}{
		{"10.16.30.15:13948", "/.env", true},
		{"10.16.40.120:33002", "/openapi.json", true},
		{"10.16.15.58:58645", "/api/config", true},
		{"10.16.29.241:7437", "/api/predict", true},
		{"10.16.7.21:34896", "/.streamlit/secrets.toml", true},
		{"10.16.18.39:48019", "/file=../.env", true},
		{"10.20.38.210:59724", "/ping", true},
		{"203.0.113.5:1234", "/.env", true},            // مسار مشبوه حتى من IP خارجي
		{"8.8.8.8:55555", "/gemini", false},            // طلب حقيقي من خارج
		{"10.16.32.103:64550", "/novel/resolve", true}, // IP داخلي حتى لمسار صحيح (scanner داخلي للمنصة)
		{"127.0.0.1:34452", "/.env", true},             // مسار مشبوه
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.path, nil)
		r.RemoteAddr = c.remote
		got := isProbe(r)
		if got != c.want {
			t.Errorf("isProbe(%s %s)=%v, want %v", c.remote, c.path, got, c.want)
		}
	}
}

// طلب فحص بلا توكن يجب أن يُرفض بـ 401 بلا جسم — لا 200 بجسم فارغ.
func TestAuth_ProbeWithoutTokenIs401NoBody(t *testing.T) {
	reached := false
	h := Auth("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

	r := httptest.NewRequest("GET", "/.env", nil)
	r.RemoteAddr = "10.16.30.15:13948"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if reached {
		t.Fatal("وصل الطلب للـ handler التالي بلا توكن")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("الجسم يجب أن يكون فارغاً: %q", rec.Body.String())
	}
}

func TestAuth_ValidTokenPassesAndPublicPathsOpen(t *testing.T) {
	h := Auth("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	r := httptest.NewRequest("GET", "/gemini", nil)
	r.RemoteAddr = "8.8.8.8:1"
	r.Header.Set("X-Internal-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("توكن صحيح: status = %d", rec.Code)
	}

	r = httptest.NewRequest("GET", "/health", nil)
	r.RemoteAddr = "8.8.8.8:1"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("/health يجب أن يمرّ بلا توكن: status = %d", rec.Code)
	}
}

func TestAuth_MissingTokenNonProbeIs401JSON(t *testing.T) {
	h := Auth("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest("POST", "/gemini", nil)
	r.RemoteAddr = "8.8.8.8:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || rec.Body.Len() == 0 {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}
