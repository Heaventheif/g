package groq

import (
	"context"

	"sunkenbot/internal/tts"
)

// groqTTS delegates Orpheus synthesis to the shared internal package while
// reusing Groq's shared key-rotation manager.
func (s *Service) groqTTS(ctx context.Context, text string) (string, error) {
	return tts.ArabicTTSWithManager(ctx, s.client, groqKeys(), text, tts.OrpheusArabicDefaultVoice())
}
