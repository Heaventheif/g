// vision.go — تحليل الصور عبر Gemini 2.5 Flash (multimodal مجاني).
//
// gemini-3.6-flash يقبل الصور كـ input مباشرة — لا حاجة لموديل منفصل.
// Free Tier: 10 RPM / 250 RPD (مشترك مع Chat).
//
// Endpoints:
//   POST /gemini/vision  — تحليل صورة + سؤال اختياري
//
// الصورة تصل إما كـ:
//   - image_url   : رابط عام يُجلب عبر netguard.SafeFetch
//   - image_base64: بيانات base64 مباشرة (مع أو بدون data URI prefix)
//
// الصيغ المدعومة: JPEG، PNG، WebP، GIF، HEIC، HEIF، BMP
// الحد الأقصى: 20MB لكل صورة (حد Gemini inline data)
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/netguard"
)

// maxVisionImageBytes: 20MB — حد Gemini inline image
const maxVisionImageBytes = 20 * 1024 * 1024

// imageMimeByExt — صيغ الصور المدعومة
var imageMimeByExt = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"webp": "image/webp",
	"gif":  "image/gif",
	"heic": "image/heic",
	"heif": "image/heif",
	"bmp":  "image/bmp",
}

func guessImageMime(ext string) string {
	if m, ok := imageMimeByExt[strings.ToLower(ext)]; ok {
		return m
	}
	return "image/jpeg"
}

// detectMimeFromBase64 — يحاول قراءة الـ mime من data URI prefix
// مثال: "data:image/png;base64,..." → "image/png"
func detectMimeFromBase64(s string) (string, string) {
	if strings.HasPrefix(s, "data:") {
		if idx := strings.Index(s, ";base64,"); idx != -1 {
			mime := s[5:idx]
			data := s[idx+8:]
			return mime, data
		}
	}
	return "", s
}

// ─── HTTP handler ──────────────────────────────────────────────────

type VisionRequest struct {
	// الصورة — أحد هذين مطلوب
	ImageURL    string `json:"image_url"`
	ImageBase64 string `json:"image_base64"`
	Ext         string `json:"ext"` // اختياري — لتحديد mime_type

	// السؤال عن الصورة — اختياري، الافتراضي "صف هذه الصورة"
	Prompt string `json:"prompt"`

	// للاستخدام مع جلسات المجموعة (اختياري)
	ThreadID   string `json:"thread_id"`
	SenderName string `json:"sender_name"`
}

type VisionResponse struct {
	Reply    string   `json:"reply"`
	Provider string   `json:"provider"`
	Sources  []string `json:"sources,omitempty"`
}

func (s *Service) handleVision(ctx context.Context, req VisionRequest) (VisionResponse, error) {
	// 1. جلب/فك تشفير الصورة
	var rawImage []byte
	var mimeType string

	switch {
	case strings.TrimSpace(req.ImageURL) != "":
		fetched, err := netguard.SafeFetch(ctx, s.client, req.ImageURL, maxVisionImageBytes)
		if err != nil {
			return VisionResponse{}, &httpx.HTTPError{
				Code: http.StatusServiceUnavailable,
				Body: map[string]any{"error": "تعذّر جلب الصورة: " + truncate(err.Error(), 200)},
			}
		}
		rawImage = fetched
		// تخمين mime من الامتداد إن وُجد
		ext := req.Ext
		if ext == "" {
			// حاول استخراج الامتداد من URL
			url := req.ImageURL
			if idx := strings.LastIndex(url, "."); idx != -1 {
				ext = strings.ToLower(url[idx+1:])
				if qIdx := strings.Index(ext, "?"); qIdx != -1 {
					ext = ext[:qIdx]
				}
			}
		}
		mimeType = guessImageMime(ext)

	case strings.TrimSpace(req.ImageBase64) != "":
		detectedMime, cleanB64 := detectMimeFromBase64(req.ImageBase64)
		decoded, err := base64.StdEncoding.DecodeString(cleanB64)
		if err != nil {
			// جرّب RawStdEncoding (بدون padding)
			decoded, err = base64.RawStdEncoding.DecodeString(cleanB64)
			if err != nil {
				return VisionResponse{}, &httpx.HTTPError{
					Code: http.StatusBadRequest,
					Body: map[string]any{"error": "image_base64 غير صالح"},
				}
			}
		}
		rawImage = decoded
		if detectedMime != "" {
			mimeType = detectedMime
		} else {
			mimeType = guessImageMime(req.Ext)
		}

	default:
		return VisionResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "image_url أو image_base64 مطلوب"},
		}
	}

	if len(rawImage) == 0 {
		return VisionResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "الصورة فارغة"},
		}
	}
	if len(rawImage) > maxVisionImageBytes {
		return VisionResponse{}, &httpx.HTTPError{
			Code: http.StatusRequestEntityTooLarge,
			Body: map[string]any{"error": "الصورة كبيرة جداً (الحد الأقصى 20MB)"},
		}
	}

	// 2. تحضير السؤال
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "صف هذه الصورة بالتفصيل باللغة العربية."
	}

	// أضف اسم المرسل إن وُجد (للجلسات الجماعية)
	if name := strings.TrimSpace(req.SenderName); name != "" {
		prompt = fmt.Sprintf("[%s]: %s", name, prompt)
	}

	// 3. استدعاء Gemini مع الصورة
	reply, sources, err := s.callGeminiVision(ctx, rawImage, mimeType, prompt)
	if err != nil {
		return VisionResponse{}, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": truncate(err.Error(), 500)},
		}
	}

	return VisionResponse{Reply: reply, Provider: "gemini", Sources: sources}, nil
}

// callGeminiVision — يرسل الصورة + النص لـ gemini-3.6-flash ويستخرج الرد.
// يستخدم نفس chatModel (gemini-3.6-flash) لأنه multimodal بطبيعته.
// Google Search Grounding مفعّل أيضاً — الموديل يستطيع البحث عن معلومات
// إضافية عن محتوى الصورة إن احتاج.
func (s *Service) callGeminiVision(ctx context.Context, imageData []byte, mimeType, prompt string) (string, []string, error) {
	keys := geminiKeys().RotatedKeys()
	if len(keys) == 0 {
		return "", nil, fmt.Errorf("لا توجد مفاتيح GEMINI_API_KEY في البيئة")
	}

	b64Image := base64.StdEncoding.EncodeToString(imageData)

	payload := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []any{map[string]any{"text": systemPrompt}},
		},
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					// الصورة أولاً ثم النص — أفضل أداء مع Gemini
					map[string]any{
						"inlineData": map[string]any{
							"mimeType": mimeType,
							"data":     b64Image,
						},
					},
					map[string]any{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens": 2048,
		},
		// Google Search Grounding مفعّل — الموديل يبحث إن احتاج
		"tools": []any{
			map[string]any{"google_search": map[string]any{}},
		},
	}

	buf, _ := json.Marshal(payload)
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent",
		chatModel,
	)

	var publicErrors []string
	for i, key := range keys {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-goog-api-key", key)

		resp, err := s.client.Do(req)
		if err != nil {
			publicErrors = append(publicErrors, fmt.Sprintf("مفتاح #%d: %s", i+1, truncate(err.Error(), 150)))
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			continue
		}
		if resp.StatusCode >= 400 {
			msg := extractGoogleErrorMessage(respBody, resp.StatusCode)
			publicErrors = append(publicErrors, fmt.Sprintf("مفتاح #%d: %s", i+1, msg))
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
			GroundingMetadata *struct {
				GroundingChunks []struct {
					Web *struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		}

		if err := json.Unmarshal(respBody, &parsed); err != nil {
			continue
		}
		if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
			continue
		}

		text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
		if text == "" {
			continue
		}

		var sources []string
		if parsed.GroundingMetadata != nil {
			for _, chunk := range parsed.GroundingMetadata.GroundingChunks {
				if chunk.Web != nil && chunk.Web.URI != "" {
					label := chunk.Web.Title
					if label == "" {
						label = chunk.Web.URI
					}
					sources = append(sources, label)
				}
			}
		}

		return text, sources, nil
	}

	return "", nil, fmt.Errorf("كل مفاتيح Gemini فشلت:\n%s", strings.Join(publicErrors, "\n"))
}
