package groq

// vision.go — فهم الصور عبر Groq.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"sunkenbot/internal/session"
)

// groqVision handles image analysis requests by cycling through vision-capable models.
// imgURL يكون إما رابط https:// (يُرسَل مباشرة، حد 20MB) أو data:mime;base64,... (حد 4MB).
func (s *Service) groqVision(ctx context.Context, messages []session.Message, imgURL, requestedModel string) (string, error) {
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}

	chatMsgs := make([]any, 0, len(messages))
	for i, m := range messages {
		if i == len(messages)-1 && m.Role == "user" {
			text := m.Content
			if text == "" {
				text = "وصف هذه الصورة"
			}
			chatMsgs = append(chatMsgs, map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": text},
					map[string]any{"type": "image_url", "image_url": map[string]any{
						"url": imgURL, // https:// أو data URI — كلاهما مقبول في Groq
					}},
				},
			})
		} else {
			chatMsgs = append(chatMsgs, map[string]any{"role": m.Role, "content": m.Content})
		}
	}

	models := visionModels
	if requestedModel != "" {
		models = []string{requestedModel}
	}
	var lastErr error
	for _, model := range models {
		payload := map[string]any{
			"model":      model,
			"messages":   chatMsgs,
			"max_tokens": 1024,
		}
		var reply string
		err := mgr.Do(func(key string) (bool, error) {
			r, status, e := s.postGroqChat(ctx, key, payload, 45*time.Second)
			if e != nil {
				return status == http.StatusTooManyRequests, e
			}
			reply = r
			return false, nil
		})
		if err == nil && reply != "" {
			return reply, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("ALL_VISION_MODELS_FAILED")
}
