package groq

// search.go — البحث في الويب.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sunkenbot/internal/session"
)

// webSearch uses Groq's current server-side browser_search tool. Compound was
// decommissioned on 2026-09-21; browser_search is its supported replacement.
func (s *Service) webSearch(ctx context.Context, messages []session.Message, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("EMPTY_SEARCH_QUERY")
	}
	searchMessages := messages
	if len(searchMessages) == 0 || searchMessages[len(searchMessages)-1].Role != "user" {
		searchMessages = append(append([]session.Message{}, messages...), session.Message{Role: "user", Content: prompt})
	}
	payload := map[string]any{
		"model":                 textModels[0],
		"messages":              toChatMessages(searchMessages),
		"tools":                 []map[string]string{{"type": "browser_search"}},
		"tool_choice":           "required",
		"max_completion_tokens": 2048,
		"temperature":           0.2,
	}
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}
	var reply string
	err := mgr.Do(func(key string) (bool, error) {
		r, status, e := s.postGroqChat(ctx, key, payload, 60*time.Second)
		if e != nil {
			return status == http.StatusTooManyRequests, e
		}
		reply = r
		return false, nil
	})
	if err != nil {
		return "", err
	}
	return reply, nil
}
