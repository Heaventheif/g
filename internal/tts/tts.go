// Package tts contains the shared Orpheus Arabic speech synthesis pipeline.
// It is intentionally independent of plugin packages.
package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"sunkenbot/internal/keyrotate"
	"sunkenbot/internal/strutil"
)

const (
	orpheusArabicModel        = "canopylabs/orpheus-arabic-saudi"
	orpheusArabicDefaultVoice = "fahad"
	ttsMaxChars               = 190
	ttsMaxAttempts            = 3
	ttsRetryBaseDelay         = 250 * time.Millisecond
	groqTTSEndpoint           = "https://api.groq.com/openai/v1/audio/speech"
)

var orpheusArabicVoices = []string{"abdullah", "fahad", "sultan", "lulwa", "noura", "aisha"}

var defaultKeys = sync.OnceValue(func() *keyrotate.Manager {
	return keyrotate.FromEnv("GROQ_API_KEY")
})

// OrpheusArabicModel returns the documented Saudi Arabic model ID.
func OrpheusArabicModel() string { return orpheusArabicModel }

// OrpheusArabicDefaultVoice returns the default Saudi Arabic voice.
func OrpheusArabicDefaultVoice() string { return orpheusArabicDefaultVoice }

// OrpheusArabicVoices returns a copy of the available Orpheus Arabic voices.
func OrpheusArabicVoices() []string {
	return append([]string(nil), orpheusArabicVoices...)
}

// ArabicTTS synthesizes Arabic speech with the default voice and environment keys.
// The result is a base64-encoded WAV file.
func ArabicTTS(ctx context.Context, client *http.Client, text string) (string, error) {
	return ArabicTTSWithVoice(ctx, client, text, orpheusArabicDefaultVoice)
}

// ArabicTTSWithVoice synthesizes Arabic speech with an explicit voice and the
// GROQ_API_KEY keys configured in the environment. An empty voice selects the default.
func ArabicTTSWithVoice(ctx context.Context, client *http.Client, text, voice string) (string, error) {
	return ArabicTTSWithManager(ctx, client, defaultKeys(), text, voice)
}

// ArabicTTSWithManager is ArabicTTSWithVoice with an explicit key manager. It
// lets the Groq plugin share its existing key-rotation state with TTS requests.
func ArabicTTSWithManager(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice string) (string, error) {
	if mgr == nil || mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}
	if voice == "" {
		voice = orpheusArabicDefaultVoice
	}
	return groqTTSGenerateV(ctx, client, mgr, text, voice)
}

// splitForTTS splits text into chunks that respect Orpheus's character limit,
// preferring sentence and word boundaries.
func splitForTTS(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var chunks []string
	runes := []rune(text)
	for len(runes) > 0 {
		if len(runes) <= ttsMaxChars {
			if piece := strings.TrimSpace(string(runes)); piece != "" {
				chunks = append(chunks, piece)
			}
			break
		}
		cut, found := ttsMaxChars, false
		for i := ttsMaxChars - 1; i > 0; i-- {
			switch runes[i] {
			case '.', '؟', '!', '،', '\n', ' ':
				cut, found = i+1, true
			}
			if found {
				break
			}
		}
		if piece := strings.TrimSpace(string(runes[:cut])); piece != "" {
			chunks = append(chunks, piece)
		}
		runes = runes[cut:]
	}
	return chunks
}

type groqTTSHTTPError struct {
	status int
	body   string
}

func (e *groqTTSHTTPError) Error() string {
	return fmt.Sprintf("groq tts status %d: %s", e.status, e.body)
}

func isRetryableGroqTTSStatus(status int) bool {
	return status == http.StatusInternalServerError || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func isRetryableGroqTTSError(err error) bool {
	var statusErr *groqTTSHTTPError
	return errors.As(err, &statusErr) && isRetryableGroqTTSStatus(statusErr.status)
}

func groqTTSChunk(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice string) ([]byte, error) {
	return groqTTSChunkAt(ctx, client, mgr, text, voice, groqTTSEndpoint)
}

// groqTTSChunkAt accepts an endpoint override for tests.
func groqTTSChunkAt(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice, endpoint string) ([]byte, error) {
	payload := map[string]any{
		"model":           orpheusArabicModel,
		"voice":           voice,
		"input":           text,
		"response_format": "wav",
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if mgr == nil || mgr.Empty() {
		return nil, keyrotate.ErrNoKeys
	}
	keys := mgr.RotatedKeys()
	var failures []string
	for attempt := 0; attempt < ttsMaxAttempts; attempt++ {
		retryRound := false
		for keyIndex, key := range keys {
			audio, err := groqTTSRequest(ctx, client, key, endpoint, buf)
			if err == nil {
				return audio, nil
			}
			failures = append(failures, fmt.Sprintf("key %d: %s", keyIndex+1, strutil.Truncate(err.Error(), 160)))
			if isRetryableGroqTTSError(err) {
				retryRound = true
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if !retryRound || attempt == ttsMaxAttempts-1 {
			break
		}
		timer := time.NewTimer(ttsRetryBaseDelay * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		keys = mgr.RotatedKeys()
	}
	return nil, fmt.Errorf("Groq TTS فشل مع جميع المفاتيح المتاحة: %s", strings.Join(failures, " | "))
}

func groqTTSRequest(ctx context.Context, client *http.Client, key, endpoint string, payload []byte) ([]byte, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &groqTTSHTTPError{status: resp.StatusCode, body: strutil.Truncate(string(respBody), 200)}
	}
	if len(respBody) == 0 {
		return nil, fmt.Errorf("groq tts returned empty audio")
	}
	return respBody, nil
}

func groqTTSGenerateV(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice string) (string, error) {
	if voice == "" {
		voice = orpheusArabicDefaultVoice
	}
	chunks := splitForTTS(text)
	if len(chunks) == 0 {
		return "", fmt.Errorf("EMPTY_TTS_TEXT")
	}
	var tmpFiles []string
	defer func() {
		for _, f := range tmpFiles {
			_ = os.Remove(f)
		}
	}()
	for i, chunk := range chunks {
		raw, err := groqTTSChunk(ctx, client, mgr, chunk, voice)
		if err != nil {
			return "", fmt.Errorf("فشل توليد الصوت للمقطع %d: %w", i+1, err)
		}
		f, err := os.CreateTemp("", fmt.Sprintf("sunkenbot-tts-%d-*.wav", i))
		if err != nil {
			return "", err
		}
		if _, err := f.Write(raw); err != nil {
			_ = f.Close()
			return "", err
		}
		_ = f.Close()
		tmpFiles = append(tmpFiles, f.Name())
	}
	if len(tmpFiles) == 1 {
		raw, err := os.ReadFile(tmpFiles[0])
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(raw), nil
	}
	listFile, err := os.CreateTemp("", "sunkenbot-tts-list-*.txt")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(listFile.Name()) }()
	for _, file := range tmpFiles {
		if _, err := fmt.Fprintf(listFile, "file '%s'\n", file); err != nil {
			_ = listFile.Close()
			return "", err
		}
	}
	if err := listFile.Close(); err != nil {
		return "", err
	}
	outPath := listFile.Name() + "_merged.wav"
	defer func() { _ = os.Remove(outPath) }()
	ctxTimeout, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctxTimeout, "ffmpeg", "-f", "concat", "-safe", "0", "-i", listFile.Name(), "-c", "copy", "-y", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg concat failed: %w (%s)", err, strutil.Truncate(string(out), 150))
	}
	merged, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(merged), nil
}
