package gemini

import (
	"encoding/json"
	"fmt"
)

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
