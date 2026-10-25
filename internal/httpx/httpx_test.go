package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type payload struct {
	A string `json:"a"`
}

// قارئ لا نهائي يعدّ ما قُرئ منه: نتأكد أن Decode يتوقف قرب الحد لا بعد استهلاك كل الجسم.
type endlessBody struct{ n *int64 }

func (e endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	atomic.AddInt64(e.n, int64(len(p)))
	return len(p), nil
}
func (endlessBody) Close() error { return nil }

func TestDecodeLimit_StopsReadingAtLimit(t *testing.T) {
	var read int64
	r := httptest.NewRequest("POST", "/", nil)
	r.Body = endlessBody{n: &read}
	_, err := DecodeLimit[payload](r, 1024)
	if err == nil || !IsTooLarge(err) {
		// الجسم «xxxx…» ليس JSON صالحاً؛ إما خطأ صياغة أو تجاوز — المهم ألا نقرأ بلا حد
		t.Logf("err = %v", err)
	}
	if read > 1024+64*1024 {
		t.Fatalf("قُرئ %d بايت رغم حد 1024", read)
	}
}

func TestDecodeLimit_OversizedValidJSONIsTooLarge(t *testing.T) {
	body := `{"a":"` + strings.Repeat("x", 5000) + `"}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	_, err := DecodeLimit[payload](r, 1024)
	if !IsTooLarge(err) {
		t.Fatalf("err = %v، المتوقع تجاوز الحد", err)
	}
}

func TestDecode_UnderLimitOK(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"hi"}`))
	v, err := Decode[payload](r)
	if err != nil || v.A != "hi" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

func TestWrapJSONLimit_413AndLargerLimitAllows(t *testing.T) {
	h := func(ctx context.Context, p payload) (map[string]int, error) {
		return map[string]int{"len": len(p.A)}, nil
	}
	body := `{"a":"` + strings.Repeat("x", 5000) + `"}`

	small := WrapJSONLimit(1024, h)
	rec := httptest.NewRecorder()
	small(rec, httptest.NewRequest("POST", "/", strings.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}

	big := WrapJSONLimit(1<<20, h)
	rec = httptest.NewRecorder()
	big(rec, httptest.NewRequest("POST", "/", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestWrapJSON_InvalidJSONStill400(t *testing.T) {
	h := WrapJSON(func(ctx context.Context, p payload) (string, error) { return "ok", nil })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/", io.NopCloser(strings.NewReader("{bad"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
