package gemini

// fallback.go — الاحتياطي عبر Groq عند فشل Gemini.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"sunkenbot/internal/session"
	"sunkenbot/internal/strutil"
)

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
		"model":      groqModel,
		"messages":   chatMsgs,
		"max_tokens": 2048,
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
				fmt.Errorf("groq status %d: %s", resp.StatusCode, strutil.Truncate(string(respBody), 200))
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
