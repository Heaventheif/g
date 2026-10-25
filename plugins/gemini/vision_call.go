package gemini

// vision_call.go — استدعاء Gemini Vision مع إعادة المحاولة.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"sunkenbot/internal/strutil"
)

const (
	maxGeminiVisionAttempts = 3
	// Google's pricing page lists Gemini 3.8 Flash as free of charge on the Free Tier.
	visionFallbackModel = "gemini-3.8-flash"
)

// callGeminiVision — يرسل الصورة + النص إلى موديل multimodal ويستخرج الرد.
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
		// لا نفعّل Google Search Grounding هنا؛ غير متاح ضمن Free Tier لموديلات Gemini 3.x.
	}

	buf, _ := json.Marshal(payload)
	var publicErrors []string
	models := []string{chatModel}
	if chatModel != visionFallbackModel {
		models = append(models, visionFallbackModel)
	}

	for _, model := range models {
		url := fmt.Sprintf(
			"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent",
			model,
		)
		modelUnavailable := false
		for keyIndex, key := range keys {
			for attempt := 0; attempt < maxGeminiVisionAttempts; attempt++ {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
				if err != nil {
					return "", nil, err
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-goog-api-key", key)

				resp, err := s.client.Do(req)
				if err != nil {
					publicErrors = append(publicErrors, fmt.Sprintf("%s / مفتاح #%d: %s", model, keyIndex+1, strutil.Truncate(err.Error(), 150)))
					if attempt+1 == maxGeminiVisionAttempts {
						modelUnavailable = true
						break
					}
					if waitErr := waitGeminiVisionRetry(ctx, attempt); waitErr != nil {
						return "", nil, waitErr
					}
					continue
				}
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				if resp.StatusCode == http.StatusTooManyRequests {
					msg := extractGoogleErrorMessage(respBody, resp.StatusCode)
					publicErrors = append(publicErrors, fmt.Sprintf("%s / مفتاح #%d: %s", model, keyIndex+1, msg))
					break // جرّب المفتاح التالي، ثم الموديل الاحتياطي عند الحاجة
				}
				if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
					msg := extractGoogleErrorMessage(respBody, resp.StatusCode)
					publicErrors = append(publicErrors, fmt.Sprintf("%s / محاولة %d: %s", model, attempt+1, msg))
					if attempt+1 == maxGeminiVisionAttempts {
						modelUnavailable = true
						break
					}
					if err := waitGeminiVisionRetry(ctx, attempt); err != nil {
						return "", nil, err
					}
					continue
				}
				if resp.StatusCode >= 400 {
					msg := extractGoogleErrorMessage(respBody, resp.StatusCode)
					publicErrors = append(publicErrors, fmt.Sprintf("%s / مفتاح #%d: %s", model, keyIndex+1, msg))
					if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
						break // قد يكون مفتاحاً غير صالح؛ جرّب المفتاح التالي
					}
					return "", nil, fmt.Errorf("%s", msg)
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

				if err := json.Unmarshal(respBody, &parsed); err != nil || len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
					publicErrors = append(publicErrors, fmt.Sprintf("%s: استجابة غير صالحة أو فارغة", model))
					modelUnavailable = true
					break
				}

				text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
				if text == "" {
					publicErrors = append(publicErrors, fmt.Sprintf("%s: استجابة فارغة", model))
					modelUnavailable = true
					break
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
		}
		if modelUnavailable {
			continue
		}
	}

	return "", nil, fmt.Errorf("كل مفاتيح Gemini فشلت:\n%s", strings.Join(publicErrors, "\n"))
}

func waitGeminiVisionRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(1<<attempt)*time.Second + time.Duration(rand.Intn(250))*time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
