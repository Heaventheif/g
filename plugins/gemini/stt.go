// stt.go — نسخة محسّنة للـ Free Tier:
//   - نموذج STT: gemini-3.6-flash (محدَّث من gemini-2.5-flash-lite)
//     السبب: gemini-2.5-flash-lite لم يعد متاحاً — Google أوقفته للمستخدمين الجدد
//     gemini-3.5-flash-lite: البديل الرسمي الأحدث بنفس مستوى الـ Free Tier
//   - حد الملف الصوتي محفوظ (19MB)
//   - منطق تدوير المفاتيح وفشل 429/503 محفوظ حرفياً
package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/netguard"
	"sunkenbot/internal/strutil"
)

// gemini-3.5-flash-lite: الموديل المحدَّث بديلاً عن gemini-2.5-flash-lite
// (gemini-2.5-flash-lite لم يعد متاحاً للمستخدمين الجدد — تحديث إلزامي)
const sttModel = "gemini-3.6-flash"

const transcribePrompt = "فرّغ (Transcribe) هذا المقطع الصوتي حرفياً وبدقة كاملة، بنفس اللغة المستخدمة في " +
	"التسجيل دون ترجمة. إن وُجد أكثر من متحدث فاذكر ذلك بإيجاز. أعد النص فقط دون أي " +
	"تعليق أو مقدمة إضافية. إن كان المقطع لا يحتوي على كلام مفهوم فأجب حرفياً بـ: " +
	"[لا يوجد كلام واضح في المقطع]"

// maxSTTAudioBytes: 19MB — حد Gemini inline data
const maxSTTAudioBytes = 19 * 1024 * 1024

// حدود جسم JSON للمسارين اللذين يحملان بيانات base64: 4/3 من الحجم الخام
// + هامش 64KB للحقول الأخرى. (الحد الافتراضي 10MB كان سيكسر الملفات الأكبر
// من ~7.5MB رغم أن الحد المسموح 19-20MB.) ثوابت — لا globals متغيّرة.
const (
	maxSTTBodyBytes    = (maxSTTAudioBytes+2)/3*4 + 64<<10
	maxVisionBodyBytes = (maxVisionImageBytes+2)/3*4 + 64<<10
)

var mimeByExt = map[string]string{
	"mp3":  "audio/mp3",
	"mp4":  "audio/mp4",
	"m4a":  "audio/mp4",
	"ogg":  "audio/ogg",
	"wav":  "audio/wav",
	"aac":  "audio/aac",
	"flac": "audio/flac",
}

func guessMimeType(ext string) string {
	if m, ok := mimeByExt[strings.ToLower(ext)]; ok {
		return m
	}
	return "audio/mp3"
}

// callGeminiSTT — نفس منطق التدوير: يستمر عند 429/503 فقط، فشل فوري لغيرها.
func (s *Service) callGeminiSTT(ctx context.Context, base64Audio, mimeType string) (string, error) {
	keys := geminiKeys().RotatedKeys()
	if len(keys) == 0 {
		return "", fmt.Errorf("لا توجد مفاتيح GEMINI_API_KEY في البيئة")
	}
	return s.callGeminiSTTWithKeys(ctx, keys, base64Audio, mimeType)
}

// callGeminiSTTWithKeys منفصلة عن callGeminiSTT ليسهل اختبارها بمفاتيح معلومة
// (geminiKeys() يُبنى مرة واحدة من البيئة عبر sync.OnceValue).
func (s *Service) callGeminiSTTWithKeys(ctx context.Context, keys []string, base64Audio, mimeType string) (string, error) {
	payload := map[string]any{
		"contents": []any{map[string]any{
			"parts": []any{
				map[string]any{"text": transcribePrompt},
				map[string]any{"inlineData": map[string]any{
					"mimeType": mimeType,
					"data":     base64Audio,
				}},
			},
		}},
		"generationConfig": map[string]any{
			// temperature/top_p/top_k محذوفة — deprecated في النماذج الحديثة
			"maxOutputTokens": 2048,
		},
	}
	buf, _ := json.Marshal(payload)
	var publicErrors []string
	for i, key := range keys {
		// المفتاح يُرسل بالترويسة داخل postGemini (لا في الـ URL — لا تسرّب في أخطاء الشبكة).
		status, respBody, err := s.postGemini(ctx, sttModel, key, buf)
		if err != nil {
			publicErrors = append(publicErrors, fmt.Sprintf("مفتاح #%d: %s", i+1, strutil.Truncate(err.Error(), 150)))
			continue
		}

		if status >= 400 {
			msg := extractGoogleErrorMessage(respBody, status)
			publicErrors = append(publicErrors, fmt.Sprintf("مفتاح #%d: %s", i+1, msg))
			if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
				return "", fmt.Errorf("%s", msg)
			}
			continue
		}

		text, _, err := parseGeminiText(respBody)
		if err == nil {
			return text, nil
		}
		if errors.Is(err, errGeminiInvalidJSON) {
			publicErrors = append(publicErrors, fmt.Sprintf("مفتاح #%d: استجابة غير صالحة", i+1))
		} else {
			publicErrors = append(publicErrors, fmt.Sprintf("مفتاح #%d: استجابة فارغة", i+1))
		}
	}

	return "", fmt.Errorf("كل مفاتيح Gemini فشلت:\n%s", strings.Join(publicErrors, "\n"))
}

// ─── HTTP handler ──────────────────────────────────────────────────

type STTRequest struct {
	AudioURL    string `json:"audio_url"`
	AudioBase64 string `json:"audio_base64"`
	Ext         string `json:"ext"`
}

type STTResponse struct {
	Transcript string `json:"transcript"`
	Model      string `json:"model"` // جديد: يُخبر العميل بالموديل المستخدم
}

func (s *Service) handleSTT(ctx context.Context, req STTRequest) (STTResponse, error) {
	var raw []byte

	switch {
	case strings.TrimSpace(req.AudioURL) != "":
		fetched, err := netguard.SafeFetch(ctx, s.client, req.AudioURL, maxSTTAudioBytes)
		if err != nil {
			return STTResponse{}, &httpx.HTTPError{
				Code: http.StatusServiceUnavailable,
				Body: map[string]any{"error": "تعذّر جلب الملف الصوتي: " + strutil.Truncate(err.Error(), 200)},
			}
		}
		raw = fetched
	case strings.TrimSpace(req.AudioBase64) != "":
		decoded, err := decodeAudioBase64(req.AudioBase64)
		if err != nil {
			return STTResponse{}, &httpx.HTTPError{
				Code: http.StatusBadRequest,
				Body: map[string]any{"error": "audio_base64 غير صالح"},
			}
		}
		raw = decoded
	default:
		return STTResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "audio_url أو audio_base64 مطلوب"},
		}
	}

	if len(raw) == 0 {
		return STTResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "الملف الصوتي فارغ"},
		}
	}
	if len(raw) > maxSTTAudioBytes {
		return STTResponse{}, &httpx.HTTPError{
			Code: http.StatusRequestEntityTooLarge,
			Body: map[string]any{"error": "الملف كبير جداً (الحد الأقصى تقريباً 19MB)"},
		}
	}

	ext := req.Ext
	if ext == "" {
		ext = "mp3"
	}
	mimeType := guessMimeType(ext)
	base64Audio := base64.StdEncoding.EncodeToString(raw)

	transcript, err := s.callGeminiSTT(ctx, base64Audio, mimeType)
	if err != nil {
		return STTResponse{}, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": strutil.Truncate(err.Error(), 500)},
		}
	}

	return STTResponse{Transcript: transcript, Model: sttModel}, nil
}

func decodeAudioBase64(s string) ([]byte, error) {
	if idx := strings.Index(s, ","); idx != -1 && strings.HasPrefix(s, "data:") {
		s = s[idx+1:]
	}
	return base64.StdEncoding.DecodeString(s)
}

// extractGoogleErrorMessage — مشترك بين stt.go وtts.go
func extractGoogleErrorMessage(body []byte, status int) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		return parsed.Error.Message
	}
	return fmt.Sprintf("HTTP %d", status)
}
