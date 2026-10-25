package gemini

// gemini_call.go — اللبنات المشتركة لاستدعاء generateContent. كانت ثلاثة نسخ
// متطابقة (بناء الطلب + فك الاستجابة + استخراج المصادر) في gemini.go و stt.go
// و vision_call.go. سياسات إعادة المحاولة والتبديل بين المفاتيح/الموديلات تبقى
// عند كل مستدعٍ لأنها مختلفة عمداً (الدردشة تتجاهل الأخطاء بصمت، الصوت يُسقط عند
// أول خطأ عميل، الرؤية تعيد المحاولة وتنتقل لموديل احتياطي).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const geminiModelsBaseURL = "https://generativelanguage.googleapis.com/v1beta/models/"

func geminiGenerateURL(model string) string {
	return geminiModelsBaseURL + model + ":generateContent"
}

var (
	errGeminiInvalidJSON  = errors.New("gemini: invalid json response")
	errGeminiNoCandidates = errors.New("gemini: no candidates in response")
	errGeminiEmptyText    = errors.New("gemini: empty text in response")
)

// postGemini ينفّذ طلب generateContent واحداً. المفتاح يُرسل بالترويسة فقط (لا
// في الـ URL) كي لا يتسرّب في رسائل أخطاء الشبكة. يُرجع رمز الحالة والجسم؛
// الخطأ هنا فشل نقل/بناء فقط، أما حالات HTTP فيقرّرها المستدعي.
func (s *Service) postGemini(ctx context.Context, model, key string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiGenerateURL(model), bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-goog-api-key", key)

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// parseGeminiText يستخرج أول نص (مقصوصاً) من الاستجابة + أسماء مصادر grounding
// إن وُجدت. الأخطاء sentinel (errGemini*) ليميّز المستدعي بين الأنواع.
func parseGeminiText(body []byte) (string, []string, error) {
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
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", nil, errGeminiInvalidJSON
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", nil, errGeminiNoCandidates
	}
	text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
	if text == "" {
		return "", nil, errGeminiEmptyText
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
