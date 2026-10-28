// vision.go — تحليل الصور عبر Gemini 2.5 Flash (multimodal مجاني).
//
// gemini-3.6-flash يقبل الصور كـ input مباشرة — لا حاجة لموديل منفصل.
// Free Tier: 10 RPM / 250 RPD (مشترك مع Chat).
//
// Endpoints:
//
//	POST /gemini/vision  — تحليل صورة + سؤال اختياري
//
// الصورة تصل إما كـ:
//   - image_url   : رابط عام يُجلب عبر netguard.SafeFetch
//   - image_base64: بيانات base64 مباشرة (مع أو بدون data URI prefix)
//
// الصيغ المدعومة: JPEG، PNG، WebP، GIF، HEIC، HEIF، BMP
// الحد الأقصى: 20MB لكل صورة (حد Gemini inline data)
package gemini

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"sunkenbot/internal/attachment"
	"sunkenbot/internal/httpx"
	"sunkenbot/internal/netguard"
	"sunkenbot/internal/strutil"
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
				Body: map[string]any{"error": "تعذّر جلب الصورة: " + strutil.Truncate(err.Error(), 200)},
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
		image, err := attachment.ParseImageBase64(req.ImageBase64, attachment.ParseOptions{
			DataURI:  attachment.DataURIBase64Only,
			Alphabet: attachment.StdPaddedOrRaw,
		})
		if err != nil {
			return VisionResponse{}, &httpx.HTTPError{
				Code: http.StatusBadRequest,
				Body: map[string]any{"error": "image_base64 غير صالح"},
			}
		}
		rawImage = image.Data
		if image.MIME != "" {
			mimeType = image.MIME
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
			Body: map[string]any{"error": strutil.Truncate(err.Error(), 500)},
		}
	}

	return VisionResponse{Reply: reply, Provider: "gemini", Sources: sources}, nil
}
