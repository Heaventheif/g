// Package groq corresponds to plugins/groq.py entirely: endpoint POST /groq
// (Flexible model routing per command type using Groq exclusively without Gemini).
package groq

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/plugins"
	"sunkenbot/internal/session"
	"sunkenbot/internal/strutil"
	"sunkenbot/internal/tts"
)

// Description defines the plugin description.
const Description = "Groq AI Bot — Flexible Multi-Model Routing (Groq Only)"

// systemPrompt sets the default behavior and persona for the AI assistant.
const systemPrompt = `أنت بوت مساعد ذكي اسمك "Sunken". أجب دائماً باللغة العربية بإيجاز (أقل من 300 كلمة). كن ودوداً ومهذباً. إذا أُرسلت إليك صورة أو صوت أو فيديو فحللها بدقة.`

// Flexible model lists for each command type, ordered by fallback preference.
var (
	textModels = []string{"openai/gpt-oss-120b", "openai/gpt-oss-20b"}
	// نماذج الرؤية المدعومة رسمياً في Groq (المصدر: console.groq.com/docs/vision)
	// ملاحظة: meta-llama/llama-4-scout و llama-4-maverick تم إيقافهما نهائياً من قبل Groq
	// (scout: يونيو 2026، maverick: فبراير 2026) ويُعيدان 404 الآن. البديل الرسمي الحالي
	// لنماذج الرؤية هو عائلة Qwen3 (تدعم image_url رسمياً حسب توثيق Groq الحالي).
	visionModels = []string{
		"qwen/qwen3.8-27b",
	}
)

// Service holds the HTTP clients and session store as explicit dependencies.
type Service struct {
	client   *http.Client
	dlClient *http.Client
	store    session.Store
}

// New creates and returns a new instance of the Groq service.
func New(client, dlClient *http.Client, store session.Store) *Service {
	return &Service{client: client, dlClient: dlClient, store: store}
}

// Name returns the unique identifier name of the plugin.
func (s *Service) Name() string { return "groq" }

// Routes registers the HTTP endpoints handled by this plugin.
func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "POST", Pattern: "/groq", Handler: httpx.WrapJSON(s.handleGroq)},
		{Method: "POST", Pattern: "/groq/stt", Handler: httpx.WrapJSONLimit(maxSTTBodyBytes, s.handleSTT)},
	}
}

// groqKeys يدير مجموعة مفاتيح Groq (تدوير Round-Robin + تبديل تلقائي عند 429)
// من متغير بيئة واحد GROQ_API_KEY، يقبل مفتاحاً واحداً أو عدة مفاتيح مفصولة
// بفواصل (مثل: key1,key2,key3). تُستخدم نفس المجموعة لكل الاستخدامات
// (دردشة/رؤية/Whisper/TTS) — نسخة واحدة تعيش في internal/tts وتتشاركها gemini
// أيضاً عبر خط TTS، فيبقى مؤشر التدوير موحَّداً.
var groqKeys = tts.Keys

// handleGroq processes incoming HTTP POST requests to the /groq endpoint.
func (s *Service) handleGroq(ctx context.Context, body map[string]any) (map[string]any, error) {
	_, hasThreadID := body["thread_id"]
	_, hasPrompt := body["prompt"]

	// Route to session-managed mode if thread_id or prompt is present.
	if hasThreadID || hasPrompt {
		return s.handleSessionMode(ctx, body)
	}

	messages := session.ParseMessages(body["messages"])
	if len(messages) == 0 {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "messages أو prompt مطلوب"},
		}
	}
	if !hasSystem(messages) {
		messages = append([]session.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	att := parseAttachment(body["attachment"])
	last := messages[len(messages)-1]
	prompt := strings.TrimSpace(strutil.StringOr(body["prompt"], last.Content))

	// Use Groq's current built-in browser_search tool when explicitly requested.
	var reply, provider string
	var err error
	if boolOr(body["web_search"]) {
		reply, err = s.webSearch(ctx, messages, prompt)
		provider = "groq-browser-search"
	} else {
		// Dispatch request based on attachment type or standard text chat.
		reply, provider, err = s.dispatchAttachment(ctx, att, messages, prompt)
	}
	if err != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": "فشلت خوادم Groq: " + strutil.Truncate(err.Error(), 100)},
		}
	}

	result := map[string]any{"reply": reply, "provider": provider}
	if wantsTTS, _ := body["tts"].(bool); wantsTTS {
		if audioB64, terr := s.groqTTS(ctx, reply); terr == nil {
			result["audio"] = audioB64
			result["audio_format"] = "wav"
		} else {
			result["tts_error"] = strutil.Truncate(terr.Error(), 150)
		}
	}
	return result, nil
}

// handleSessionMode manages chat history sessions and state clearing.
func (s *Service) handleSessionMode(ctx context.Context, body map[string]any) (map[string]any, error) {
	threadID := strutil.StringOr(body["thread_id"], "default")
	senderName := strutil.StringOr(body["sender_name"], "مستخدم")
	prompt := strings.TrimSpace(strutil.StringOr(body["prompt"], ""))
	doClear, _ := body["clear"].(bool)
	att := parseAttachment(body["attachment"])
	webSearch := boolOr(body["web_search"])

	// Clear session history if requested.
	if doClear {
		_ = s.store.Clear(ctx, threadID)
		return map[string]any{"reply": "🧹 تم مسح ذاكرة المجموعة."}, nil
	}

	ctxMsgs, _ := s.store.Load(ctx, threadID)
	userContent := fmt.Sprintf("[%s]: %s", senderName, prompt)
	if prompt == "" {
		userContent = fmt.Sprintf("[%s]: ما هذا؟", senderName)
	}

	messages := append([]session.Message{{Role: "system", Content: systemPrompt}}, ctxMsgs...)
	messages = append(messages, session.Message{Role: "user", Content: userContent})

	var reply, provider string
	var err error
	if webSearch {
		reply, err = s.webSearch(ctx, messages, prompt)
		provider = "groq-browser-search"
	} else {
		reply, provider, err = s.dispatchAttachment(ctx, att, messages, prompt)
	}
	if err != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": "فشلت خوادم Groq: " + strutil.Truncate(err.Error(), 100)},
		}
	}

	attLabel := ""
	if att != nil {
		attLabel = fmt.Sprintf("[%s] ", att.Kind)
	}
	userText := strings.TrimSpace(fmt.Sprintf("[%s]: %s%s", senderName, attLabel, prompt))

	// Save updated conversation history to the session store.
	newHistory := append(append([]session.Message{}, ctxMsgs...),
		session.Message{Role: "user", Content: userText},
		session.Message{Role: "assistant", Content: reply},
	)
	_ = s.store.Save(ctx, threadID, newHistory)

	result := map[string]any{"reply": reply, "provider": provider}
	if wantsTTS, _ := body["tts"].(bool); wantsTTS {
		if audioB64, terr := s.groqTTS(ctx, reply); terr == nil {
			result["audio"] = audioB64
			result["audio_format"] = "wav"
		} else {
			result["tts_error"] = strutil.Truncate(terr.Error(), 150)
		}
	}
	return result, nil
}

// dispatchAttachment routes the request to the correct handler depending on the attachment type.
func (s *Service) dispatchAttachment(ctx context.Context, att *attachment, messages []session.Message, prompt string) (reply, provider string, err error) {
	if att == nil {
		reply, err = s.tryModels(ctx, textModels, messages, 30*time.Second)
		return reply, "groq-text", err
	}

	switch att.Kind {
	case "image":
		mime := att.ContentType
		if mime == "" {
			mime = "image/jpeg"
		}

		var imgURL string
		switch {
		case att.URL != "":
			// أفضل طريقة: إرسال الرابط مباشرة إلى Groq (يقبل https:// حتى 20MB)
			// تجنّب تحميل الصورة وإعادة ترميزها base64 الذي يخضع لحد 4MB.
			imgURL = att.URL
			mime = guessMime(att.URL, nil) // استنتاج النوع من الامتداد

		case att.Base64 != "":
			// التحقق من الحجم قبل الإرسال — Groq يُعيد 413 إذا تجاوز 4MB
			rawSize := estimateBase64DecodedLen(att.Base64)
			if rawSize > maxBase64VisionBytes {
				return "", "", fmt.Errorf(
					"حجم الصورة (~%dMB) يتجاوز حد Groq للصور (4MB). أرسل رابط URL بدلاً من base64",
					rawSize/(1024*1024),
				)
			}
			imgURL = fmt.Sprintf("data:%s;base64,%s", mime, att.Base64)

		default:
			break
		}

		if imgURL != "" {
			reply, err = s.groqVision(ctx, messages, imgURL, att.Model)
			return reply, "groq-vision", err
		}

	// ملاحظة: Kind == "audio" غير معالَج هنا عن قصد — يسقط إلى دردشة نصية
	// بلا مرفق. تفريغ الصوت يتم عبر POST /groq/stt (handleSTT) ثم يُرسل النص
	// الناتج كـ prompt عادي. (قرار المالك؛ أضف فرعاً هنا فقط بعد تغيير هذا العقد.)
	case "video":
		if att.URL == "" {
			break
		}
		reply, err = s.processVideo(ctx, att.URL, prompt, messages)
		return reply, "groq-video", err
	}

	reply, err = s.tryModels(ctx, textModels, messages, 30*time.Second)
	return reply, "groq-text", err
}
