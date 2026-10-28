package translation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	translator "gopkg.gilang.dev/translator/v2"
)

type fakeTranslator struct{}

func (fakeTranslator) Translate(_ context.Context, text, from, to string) (*translator.Translated, error) {
	return &translator.Translated{Text: "translated:" + text}, nil
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	bundle, err := loadBundle()
	if err != nil {
		t.Fatalf("loadBundle() error = %v", err)
	}
	backend := fakeTranslator{}
	return newService(bundle, map[string]translator.Translator{
		"google":       backend,
		"deepl":        backend,
		"go-translate": backend,
		"microsoft":    backend,
	})
}

func invokeRoute(s *Service, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	routes := s.Routes()
	var handler http.HandlerFunc
	for _, route := range routes {
		if route.Method == method && (route.Pattern == path || strings.HasPrefix(path, strings.TrimSuffix(route.Pattern, "/")+"/")) {
			handler = route.Handler
			break
		}
	}
	recorder := httptest.NewRecorder()
	if handler == nil {
		recorder.WriteHeader(http.StatusNotFound)
		return recorder
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestHelloRespectsAcceptLanguage(t *testing.T) {
	s := newTestService(t)
	response := invokeRoute(s, http.MethodGet, "/hello/Ali", "", map[string]string{"Accept-Language": "ar"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var result struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Message != "مرحبًا، Ali!" {
		t.Fatalf("message = %q, want Arabic greeting", result.Message)
	}
}

func TestTranslateSelectsProvider(t *testing.T) {
	s := newTestService(t)
	response := invokeRoute(s, http.MethodPost, "/translate", `{"text":"Hello","to":"ar","provider":"deepl"}`, map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var result translateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Text != "translated:Hello" || result.Provider != "deepl" || result.To != "ar" {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestTranslateValidatesRequiredFields(t *testing.T) {
	s := newTestService(t)
	response := invokeRoute(s, http.MethodPost, "/translate", `{"text":"Hello"}`, map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestGoTranslateProvider(t *testing.T) {
	s := newTestService(t)
	response := invokeRoute(s, http.MethodPost, "/translate", `{"text":"Hello","to":"ar","provider":"go-translate"}`, map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var result translateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Text != "translated:Hello" || result.Provider != "go-translate" {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestMicrosoftRequiresSourceLanguage(t *testing.T) {
	s := newTestService(t)
	response := invokeRoute(s, http.MethodPost, "/translate", `{"text":"Hello","to":"ar","provider":"microsoft"}`, map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}
