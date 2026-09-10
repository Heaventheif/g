// Package gemini — نسخة محسّنة للـ Free Tier:
//   - نموذج الدردشة: gemini-3.6-flash (الأفضل مجاناً: 10 RPM / 250 RPD)
//   - Google Search Grounding مفعّل دائماً → اتصال بالإنترنت في الوقت الفعلي
//   - حُذفت temperature/top_p/top_k (deprecated في النماذج الحديثة)
//   - maxOutputTokens رُفع من 1024 → 2048 (مجاني، لا يكلف شيئاً)
//   - Groq fallback محفوظ كما هو
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/keyrotate"
	"sunkenbot/internal/plugins"
	"sunkenbot/internal/session"
)

const Description = "Gemini 2.5 Flash (Multimodal) — دردشة + تحليل صور + بحث فعلي بالإنترنت + TTS/STT — جلسات جماعية + Groq fallback"

const (
	// نموذج الدردشة الرئيسي — أفضل موديل مجاني متاح حالياً.
	// Free Tier: 10 RPM / 250 RPD / 250k TPM
	chatModel = "gemini-3.6-flash"

	// Groq fallback — يُستخدم عند استنفاد كل مفاتيح Gemini
	// ملاحظة: llama-3.3-70b-versatile أوقفته Groq نهائياً — البديل الرسمي
	// الحالي الذي توصي به Groq هو openai/gpt-oss-120b.
	groqModel = "openai/gpt-oss-120b"

	systemPrompt = `أنت بوت مساعد ذكي اسمك "Sunken". أجب باللغة العربية بإيجاز (أقل من 200 كلمة).
كن ودوداً ومفيداً. عند السؤال عن أحداث جارية أو أخبار أو معلومات تتغير بمرور الوقت،
استخدم بحث Google للحصول على معلومات محدّثة وأذكر المصدر.`
)

// ─── Service ───────────────────────────────────────────────────────

type Service struct {
	client *http.Client
	store  session.Store
}

func New(client *http.Client, store session.Store) *Service {
	return &Service{client: client, store: store}
}

func (s *Service) Name() string { return "gemini" }

func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "POST", Pattern: "/gemini", Handler: httpx.Handle(s.handleGemini)},
		{Method: "POST", Pattern: "/gemini/tts", Handler: httpx.WrapJSON(s.handleTTS)},
		{Method: "GET", Pattern: "/gemini/tts/voices", Handler: httpx.Handle(s.handleTTSVoices)},
		{Method: "POST", Pattern: "/gemini/stt", Handler: httpx.WrapJSON(s.handleSTT)},
		// بحث صريح بالإنترنت بدون ذاكرة جلسة
		{Method: "POST", Pattern: "/gemini/search", Handler: httpx.Handle(s.handleSearch)},
		// تحليل الصور (multimodal) — gemini-3.6-flash مجاناً
		{Method: "POST", Pattern: "/gemini/vision", Handler: httpx.WrapJSON(s.handleVision)},
	}
}

// ─── مفاتيح البيئة ─────────────────────────────────────────────────

// geminiKeys يدير مجموعة مفاتيح Gemini (تدوير Round-Robin + تبديل تلقائي عند
// 429) — يُبنى مرة واحدة فقط. يدعم كلا النمطين معاً للتوافق الخلفي:
//   - عدة متغيرات بيئة منفصلة: GEMINI_API_KEY, GEMINI_API_KEY_2, _3, _4 (كما كانت)
//   - أو مفاتيح متعددة مفصولة بفواصل داخل أيٍّ من هذه المتغيرات (مثل
//     GEMINI_API_KEY=key1,key2,key3) — الأسلوب المفضّل للإضافة/الحذف السريع.
var geminiKeys = sync.OnceValue(func() *keyrotate.Manager {
	return keyrotate.FromEnvAny(
		"GEMINI_API_KEY", "GEMINI_API_KEY_2",
		"GEMINI_API_KEY_3", "GEMINI_API_KEY_4",
	)
})

// groqKeys يدير مجموعة مفاتيح Groq المستخدمة هنا فقط كخط احتياط (fallback)
// عند استنفاد كل مفاتيح Gemini — GROQ_API_KEY يقبل مفتاحاً واحداً أو عدة
// مفاتيح مفصولة بفواصل.
var groqKeys = sync.OnceValue(func() *keyrotate.Manager {
	return keyrotate.FromEnv("GROQ_API_KEY")
})

// ─── HTTP handlers ─────────────────────────────────────────────────

func (s *Service) handleGemini(r *http.Request) (map[string]any, error) {
	ctx := r.Context()

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusInternalServerError,
			Body: map[string]any{"error": truncate(err.Error(), 200)},
		}
	}

	_, hasThreadID := body["thread_id"]
	_, hasPrompt := body["prompt"]

	if hasThreadID || hasPrompt {
		return s.handleSessionMode(ctx, body)
	}

	// النمط القديم: messages مباشرة
	messages := parseMessages(body["messages"])
	if len(messages) == 0 {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "messages أو prompt مطلوب"},
		}
	}

	reply, sources, err := s.callGemini(ctx, messages)
	if err == nil {
		resp := map[string]any{"reply": reply, "provider": "gemini"}
		if len(sources) > 0 {
			resp["sources"] = sources
		}
		return resp, nil
	}

	reply, err2 := s.callGroq(ctx, ensureSystem(messages))
	if err2 != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": "كل الخوادم فشلت: " + truncate(err2.Error(), 100)},
		}
	}
	return map[string]any{"reply": reply, "provider": "groq"}, nil
}

// handleSearch — endpoint جديد: بحث مباشر بالإنترنت بدون تاريخ محادثة.
// مثالي لـ .search <سؤال> في بوت الميسنجر.
func (s *Service) handleSearch(r *http.Request) (map[string]any, error) {
	ctx := r.Context()

	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Query) == "" {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "query مطلوب"},
		}
	}

	searchPrompt := "ابحث في الإنترنت وأجب على هذا السؤال بمعلومات محدّثة واذكر المصادر: " + body.Query
	messages := []session.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: searchPrompt},
	}

	reply, sources, err := s.callGemini(ctx, messages)
	if err != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": truncate(err.Error(), 200)},
		}
	}

	resp := map[string]any{"reply": reply, "provider": "gemini", "grounded": true}
	if len(sources) > 0 {
		resp["sources"] = sources
	}
	return resp, nil
}

func (s *Service) handleSessionMode(ctx context.Context, body map[string]any) (map[string]any, error) {
	threadID := stringOr(body["thread_id"], "default")
	senderName := stringOr(body["sender_name"], "مستخدم")
	prompt := strings.TrimSpace(stringOr(body["prompt"], ""))
	doClear, _ := body["clear"].(bool)

	if doClear {
		_ = s.store.Clear(ctx, threadID)
		return map[string]any{"reply": "🧹 تم مسح ذاكرة المجموعة."}, nil
	}

	if prompt == "" {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "prompt مطلوب"},
		}
	}

	ctxMsgs, _ := s.store.Load(ctx, threadID)
	userContent := fmt.Sprintf("[%s]: %s", senderName, prompt)

	messages := append([]session.Message{{Role: "system", Content: systemPrompt}}, ctxMsgs...)
	messages = append(messages, session.Message{Role: "user", Content: userContent})

	var reply, provider string
	var sources []string

	reply, sources, err := s.callGemini(ctx, messages)
	if err == nil {
		provider = "gemini"
	} else {
		reply, err = s.callGroq(ctx, ensureSystem(messages))
		if err != nil {
			return nil, &httpx.HTTPError{
				Code: http.StatusServiceUnavailable,
				Body: map[string]any{"error": "كل الخوادم فشلت: " + truncate(err.Error(), 100)},
			}
		}
		provider = "groq"
	}

	newHistory := append(append([]session.Message{}, ctxMsgs...),
		session.Message{Role: "user", Content: userContent},
		session.Message{Role: "assistant", Content: reply},
	)
	_ = s.store.Save(ctx, threadID, newHistory)

	resp := map[string]any{"reply": reply, "provider": provider}
	if len(sources) > 0 {
		resp["sources"] = sources
	}
	return resp, nil
}

// ─── Gemini REST (مع Google Search Grounding) ──────────────────────
//
// Google Search Grounding متاح مجاناً في Free Tier ويجعل الموديل
// يبحث تلقائياً في الإنترنت عند الحاجة — هذه هي ميزة "الاتصال الفعلي".
// الموديل يقرر بنفسه متى يبحث (للأسئلة الآنية) ومتى يجيب من ذاكرته.

func (s *Service) callGemini(ctx context.Context, messages []session.Message) (reply string, sources []string, err error) {
	contents := toGeminiContents(messages)

	payload := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": systemPrompt}},
		},
		"contents": contents,
		"generationConfig": map[string]any{
			// ملاحظة: temperature/top_p/top_k محذوفة — deprecated في النماذج الحديثة.
			// رُفع الحد من 1024 إلى 2048 — مجاني تماماً.
			"maxOutputTokens": 2048,
		},
		// Google Search Grounding — الاتصال الفعلي بالإنترنت في الوقت الفعلي.
		// متاح مجاناً ضمن Free Tier ولا يُضاف له حساب منفصل.
		"tools": []any{
			map[string]any{"google_search": map[string]any{}},
		},
	}
	buf, _ := json.Marshal(payload)

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent",
		chatModel,
	)

	for _, key := range geminiKeys().RotatedKeys() {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
		if e != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-goog-api-key", key)

		resp, e := s.client.Do(req)
		if e != nil {
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			continue // جرّب المفتاح التالي
		}
		if resp.StatusCode >= 400 {
			continue
		}

		var parsed struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			// استخراج مصادر البحث من groundingMetadata
			GroundingMetadata *struct {
				SearchEntryPoint *struct {
					RenderedContent string `json:"renderedContent"`
				} `json:"searchEntryPoint"`
				GroundingChunks []struct {
					Web *struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		}

		if e := json.Unmarshal(respBody, &parsed); e != nil {
			continue
		}
		if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
			continue
		}

		text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
		if text == "" {
			continue
		}

		// استخراج مصادر البحث إن وُجدت
		var srcs []string
		if parsed.GroundingMetadata != nil {
			for _, chunk := range parsed.GroundingMetadata.GroundingChunks {
				if chunk.Web != nil && chunk.Web.URI != "" {
					label := chunk.Web.Title
					if label == "" {
						label = chunk.Web.URI
					}
					srcs = append(srcs, label)
				}
			}
		}

		return text, srcs, nil
	}

	return "", nil, fmt.Errorf("ALL_GEMINI_KEYS_EXHAUSTED")
}

func toGeminiContents(messages []session.Message) []map[string]any {
	contents := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []any{map[string]any{"text": m.Content}},
		})
	}
	return contents
}

// ─── Groq fallback ─────────────────────────────────────────────────

func (s *Service) callGroq(ctx context.Context, messages []session.Message) (string, error) {
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}

	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	chatMsgs := make([]chatMsg, len(messages))
	for i, m := range messages {
		chatMsgs[i] = chatMsg{Role: m.Role, Content: m.Content}
	}

	payload := map[string]any{
		"model":       groqModel,
		"messages":    chatMsgs,
		"max_tokens":  2048,
		// temperature محفوظة هنا فقط لأن Groq لا يزال يدعمها
		"temperature": 0.7,
	}
	buf, _ := json.Marshal(payload)

	var reply string
	err := mgr.Do(func(key string) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(buf))
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 {
			return resp.StatusCode == http.StatusTooManyRequests,
				fmt.Errorf("groq status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
		}

		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return false, err
		}
		if len(parsed.Choices) == 0 {
			return false, fmt.Errorf("empty choices")
		}
		reply = parsed.Choices[0].Message.Content
		return false, nil
	})
	if err != nil {
		return "", err
	}
	return reply, nil
}

// ─── أدوات مساعدة ──────────────────────────────────────────────────

func ensureSystem(messages []session.Message) []session.Message {
	for _, m := range messages {
		if m.Role == "system" {
			return messages
		}
	}
	return append([]session.Message{{Role: "system", Content: systemPrompt}}, messages...)
}

func parseMessages(raw any) []session.Message {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]session.Message, 0, len(arr))
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, session.Message{
			Role:    stringOr(obj["role"], ""),
			Content: stringOr(obj["content"], ""),
		})
	}
	return out
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
