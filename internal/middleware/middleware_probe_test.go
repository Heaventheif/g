package middleware

import (
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
		{"203.0.113.5:1234", "/.env", true},          // مسار مشبوه حتى من IP خارجي
		{"8.8.8.8:55555", "/gemini", false},          // طلب حقيقي من خارج
		{"10.16.32.103:64550", "/novel/resolve", true},   // IP داخلي حتى لمسار صحيح (scanner داخلي للمنصة)
		{"127.0.0.1:34452", "/.env", true},           // مسار مشبوه
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
