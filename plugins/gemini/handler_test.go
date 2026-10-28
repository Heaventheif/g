package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// جسم JSON غير صالح يمر عبر WrapJSON ويعيد خطأ 400 موحداً.
func TestHandleGemini_InvalidJSONIs400(t *testing.T) {
	s := &Service{}
	req := httptest.NewRequest("POST", "/gemini", strings.NewReader("{not json"))
	var handler http.Handler
	for _, route := range s.Routes() {
		if route.Pattern == "/gemini" {
			handler = route.Handler
			break
		}
	}
	if handler == nil {
		t.Fatal("/gemini route not found")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error":"invalid request body"`) {
		t.Fatalf("status/body = %d %s, want normalized 400", rec.Code, rec.Body.String())
	}
}

// حالة TTS صفرية القيمة تعمل (الاختبارات تبني &Service{}) والكاش LRU يطرد الأقدم.
func TestTTSState_ZeroValueCacheAndEviction(t *testing.T) {
	var st ttsState
	if _, _, ok := st.fromCache("k"); ok {
		t.Fatal("كاش فارغ يجب ألا يعيد شيئاً")
	}
	for i := range ttsCacheMaxSize + 1 {
		st.toCache(string(rune('A'+i)), []byte{byte(i)}, "v")
	}
	if _, _, ok := st.fromCache(string(rune('A'))); ok {
		t.Fatal("أقدم عنصر كان يجب أن يُطرد")
	}
	if _, _, ok := st.fromCache(string(rune('A' + ttsCacheMaxSize))); !ok {
		t.Fatal("أحدث عنصر يجب أن يبقى")
	}
}

func TestTTSState_NextVoiceCyclesThroughPool(t *testing.T) {
	var st ttsState
	seen := map[string]bool{}
	for range 6 {
		seen[st.nextVoice()] = true
	}
	if len(seen) != 6 {
		t.Fatalf("متوقع 6 أصوات مختلفة قبل إعادة التعبئة، حصلت على %d", len(seen))
	}
}
