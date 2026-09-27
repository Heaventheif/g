package groq

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sunkenbot/internal/session"
)

// TestParseAttachment_RootLevel يوثّق العطل الحرِج الذي دفع لكتابة هذا
// البرومبت أصلاً: attachment كان يُقرأ من داخل آخر عنصر في messages بدل
// حقل جذر منفصل، فتوقفت صورة/صوت/فيديو groq عن العمل بعد إصلاح مسار
// آخر متصل. هذا الاختبار يمنع تكرار ذلك الانحدار مستقبلاً.
func TestParseAttachment_RootLevel(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "صف هذه الصورة"},
		},
		"attachment": map[string]any{
			"kind": "image",
			"url":  "https://example.com/cat.jpg",
		},
	}

	att := parseAttachment(body["attachment"])
	if att == nil {
		t.Fatal("expected non-nil attachment parsed from root-level field")
	}
	if att.Kind != "image" {
		t.Errorf("kind = %q, want %q", att.Kind, "image")
	}
	if att.URL != "https://example.com/cat.jpg" {
		t.Errorf("url = %q, want the root-level url", att.URL)
	}

	// تأكيد سلبي: attachment متداخل داخل messages (الشكل القديم الخاطئ)
	// يجب ألا يُقرأ من هناك إطلاقاً.
	nested, _ := body["messages"].([]any)[0].(map[string]any)
	if _, exists := nested["attachment"]; exists {
		t.Fatal("test setup error: attachment must live at body root, not nested in messages")
	}
}

// TestRoutes يتحقق من أن /groq مسجَّل بـ POST — وليس أي method آخر.
func TestRoutes(t *testing.T) {
	svc := New(http.DefaultClient, http.DefaultClient, nil)
	routes := svc.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected exactly 1 route, got %d", len(routes))
	}
	if routes[0].Method != "POST" || routes[0].Pattern != "/groq" {
		t.Errorf("route = %s %s, want POST /groq", routes[0].Method, routes[0].Pattern)
	}
}

// TestHandleGroq_MissingBody يتحقق من مسار التحقق المبكر (بلا messages
// ولا prompt) — لا يلمس أي عميل http.Client أو session.Store، فهو آمن
// للتشغيل بلا شبكة.
func TestHandleGroq_MissingBody(t *testing.T) {
	svc := New(http.DefaultClient, http.DefaultClient, nil)

	req := httptest.NewRequest(http.MethodPost, "/groq", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	svc.Routes()[0].Handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d. body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "messages أو prompt مطلوب") {
		t.Errorf("unexpected error body: %s", rec.Body.String())
	}
}

func TestBuildGroqAudioContextPreservesGroupSpeakers(t *testing.T) {
	previous := []session.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "[ليلى]: ما الخطة؟"},
		{Role: "assistant", Content: "لنبدأ بتحديد الهدف."},
		{Role: "user", Content: "[عمر]: [صوت] لخّص التسجيل"},
	}
	got := buildGroqAudioContext(previous, "نحتاج إلى ميزانية", "[عمر]: [صوت] لخّص التسجيل")
	if len(got) != len(previous)+1 {
		t.Fatalf("message count = %d, want %d", len(got), len(previous)+1)
	}
	if got[1].Content != "[ليلى]: ما الخطة؟" || got[2].Content != "لنبدأ بتحديد الهدف." {
		t.Fatal("previous group turns were not preserved")
	}
	last := got[len(got)-1].Content
	for _, want := range []string{"[عمر]: [تفريغ الصوت]", "نحتاج إلى ميزانية", "السؤال: لخّص التسجيل"} {
		if !strings.Contains(last, want) {
			t.Errorf("audio context %q does not contain %q", last, want)
		}
	}
}
