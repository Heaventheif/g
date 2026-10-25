package groq

// chat.go — محادثة Groq: تجربة النماذج وتحويل الرسائل.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sunkenbot/internal/session"
	"sunkenbot/internal/strutil"
)

// tryModels iterates through a list of models in descending priority, and for each
// model rotates through all available Groq keys (Round-Robin start point + automatic
// switch-and-retry on 429) until one model/key combination succeeds.
func (s *Service) tryModels(ctx context.Context, models []string, messages []session.Message, timeout time.Duration) (string, error) {
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}

	var lastErr error
	for _, model := range models {
		payload := map[string]any{
			"model":       model,
			"messages":    toChatMessages(messages),
			"max_tokens":  1024,
			"temperature": 0.7,
		}
		var reply string
		err := mgr.Do(func(key string) (bool, error) {
			r, status, e := s.postGroqChat(ctx, key, payload, timeout)
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
	return "", fmt.Errorf("ALL_MODELS_FAILED")
}

// postGroqChat sends a chat completion request to the Groq API endpoint. It also
// returns the HTTP status code (0 on network-level failure) so callers can detect
// rate limiting (429) and switch to the next key via keyrotate.Manager.Do.
func (s *Service) postGroqChat(ctx context.Context, key string, payload map[string]any, timeout time.Duration) (string, int, error) {
	buf, _ := json.Marshal(payload)
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", resp.StatusCode, fmt.Errorf("groq status %d: %s", resp.StatusCode, strutil.Truncate(string(respBody), 200))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", resp.StatusCode, err
	}
	if len(parsed.Choices) == 0 {
		return "", resp.StatusCode, fmt.Errorf("empty choices")
	}
	return stripThinking(parsed.Choices[0].Message.Content), resp.StatusCode, nil
}

// stripThinking removes <think>...</think> reasoning traces that some models (e.g. qwen
// reasoning models) prepend to their output. Handles an unclosed trailing <think> too,
// in case the response was truncated mid-thought.
func stripThinking(text string) string {
	for {
		start := strings.Index(text, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], "</think>")
		if end == -1 {
			// Unclosed think block: drop everything from <think> onward.
			text = text[:start]
			break
		}
		text = text[:start] + text[start+end+len("</think>"):]
	}
	return strings.TrimSpace(text)
}

// hasSystem checks if a system prompt is already included in the message slice.
func hasSystem(messages []session.Message) bool {
	for _, m := range messages {
		if m.Role == "system" {
			return true
		}
	}
	return false
}

// toChatMessages converts domain session messages into standard map formats for API payloads.
func toChatMessages(messages []session.Message) []map[string]string {
	out := make([]map[string]string, len(messages))
	for i, m := range messages {
		out[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	return out
}

// parseMessages decodes raw interface arrays into structured session messages.
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
			Role:    strutil.StringOr(obj["role"], ""),
			Content: strutil.StringOr(obj["content"], ""),
		})
	}
	return out
}

func boolOr(v any) bool {
	b, _ := v.(bool)
	return b
}
