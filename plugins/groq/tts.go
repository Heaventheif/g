package groq

// tts.go — تحويل النص إلى صوت (Orpheus العربي).

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
	"time"

	"sunkenbot/internal/keyrotate"
	"sunkenbot/internal/strutil"
)

// splitForTTS breaks text into chunks that respect Orpheus's per-request character limit,
// preferring to break at sentence or word boundaries rather than mid-word.
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
		// نبحث نزولاً من آخر فهرس مسموح (ttsMaxChars-1) حتى لا يتجاوز المقطع الحد،
		// ونستخدم found بدل قيمة سحرية: فاصل عند الفهرس 189 يعطي cut == ttsMaxChars
		// وهي قيمة شرعية يجب ألا تُفسَّر كـ «لم يُعثر على فاصل».
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

// groqTTSChunk synthesizes a single Orpheus chunk, trying configured Groq API
// keys in rotation and retrying transient 5xx responses before returning an error.
func groqTTSChunk(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice string) ([]byte, error) {
	return groqTTSChunkAt(ctx, client, mgr, text, voice, groqTTSEndpoint)
}

// groqTTSChunkAt accepts an endpoint override so retries can be tested without
// reaching the live Groq API.
func groqTTSChunkAt(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice, endpoint string) ([]byte, error) {
	payload := map[string]any{
		"model":           ttsModel,
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

// orpheusArabicVoices: الأصوات المتاحة في canopylabs/orpheus-arabic-saudi على Groq.
// أضف المزيد هنا عند إتاحتها رسمياً.
var orpheusArabicVoices = []string{"abdullah", "fahad", "sultan", "lulwa", "noura", "aisha"}

// OrpheusArabicModel returns the documented Saudi Arabic model ID.
func OrpheusArabicModel() string { return ttsModel }

// OrpheusArabicDefaultVoice returns the service's default Saudi Arabic voice.
func OrpheusArabicDefaultVoice() string { return ttsVoice }

// OrpheusArabicVoices يُعيد نسخة من قائمة أصوات Orpheus للاستخدام الخارجي.
func OrpheusArabicVoices() []string {
	return append([]string(nil), orpheusArabicVoices...)
}

// ArabicTTS يعرّض خط أنابيب Orpheus العربي على Groq (تقطيع + دمج ffmpeg) كدالة
// عامة قابلة للاستخدام من بلجنات أخرى. يرجّع صوت WAV بصيغة base64.
func ArabicTTS(ctx context.Context, client *http.Client, text string) (string, error) {
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}
	return groqTTSGenerate(ctx, client, mgr, text)
}

// ArabicTTSWithVoice مثل ArabicTTS لكن مع تحديد الصوت صراحةً.
// إذا كان voice فارغاً يُستخدم الصوت الافتراضي.
func ArabicTTSWithVoice(ctx context.Context, client *http.Client, text, voice string) (string, error) {
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}
	if voice == "" {
		voice = ttsVoice
	}
	return groqTTSGenerateV(ctx, client, mgr, text, voice)
}

// groqTTSGenerateV مثل groqTTSGenerate لكن يمرّر voice لكل مقطع.
func groqTTSGenerateV(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text, voice string) (string, error) {
	chunks := splitForTTS(text)
	if len(chunks) == 0 {
		return "", fmt.Errorf("EMPTY_TTS_TEXT")
	}
	var tmpFiles []string
	defer func() {
		for _, f := range tmpFiles {
			os.Remove(f)
		}
	}()
	for i, c := range chunks {
		raw, err := groqTTSChunk(ctx, client, mgr, c, voice)
		if err != nil {
			return "", fmt.Errorf("فشل توليد الصوت للمقطع %d: %w", i+1, err)
		}
		f, err := os.CreateTemp("", fmt.Sprintf("sunkenbot-tts-%d-*.wav", i))
		if err != nil {
			return "", err
		}
		if _, err := f.Write(raw); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
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
	defer os.Remove(listFile.Name())
	for _, f := range tmpFiles {
		fmt.Fprintf(listFile, "file '%s'\n", f)
	}
	listFile.Close()
	outPath := listFile.Name() + "_merged.wav"
	defer os.Remove(outPath)
	ctxTimeout, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctxTimeout, "ffmpeg",
		"-f", "concat", "-safe", "0", "-i", listFile.Name(), "-c", "copy", "-y", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg concat failed: %w (%s)", err, strutil.Truncate(string(out), 150))
	}
	merged, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(merged), nil
}

// groqTTS converts arbitrary-length text to speech, chunking as needed for Orpheus's
// character limit and stitching multi-chunk replies into a single wav file via ffmpeg.
// Returns base64-encoded wav audio.
func (s *Service) groqTTS(ctx context.Context, text string) (string, error) {
	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}
	return groqTTSGenerate(ctx, s.client, mgr, text)
}

func groqTTSGenerate(ctx context.Context, client *http.Client, mgr *keyrotate.Manager, text string) (string, error) {
	chunks := splitForTTS(text)
	if len(chunks) == 0 {
		return "", fmt.Errorf("EMPTY_TTS_TEXT")
	}

	var tmpFiles []string
	defer func() {
		for _, f := range tmpFiles {
			os.Remove(f)
		}
	}()

	for i, c := range chunks {
		raw, err := groqTTSChunk(ctx, client, mgr, c, ttsVoice)
		if err != nil {
			return "", fmt.Errorf("فشل توليد الصوت للمقطع %d: %w", i+1, err)
		}
		f, err := os.CreateTemp("", fmt.Sprintf("sunkenbot-tts-%d-*.wav", i))
		if err != nil {
			return "", err
		}
		if _, err := f.Write(raw); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		tmpFiles = append(tmpFiles, f.Name())
	}

	if len(tmpFiles) == 1 {
		raw, err := os.ReadFile(tmpFiles[0])
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(raw), nil
	}

	// Multiple chunks: concatenate into one wav using ffmpeg's concat demuxer.
	listFile, err := os.CreateTemp("", "sunkenbot-tts-list-*.txt")
	if err != nil {
		return "", err
	}
	defer os.Remove(listFile.Name())
	for _, f := range tmpFiles {
		fmt.Fprintf(listFile, "file '%s'\n", f)
	}
	listFile.Close()

	outPath := listFile.Name() + "_merged.wav"
	defer os.Remove(outPath)

	ctxTimeout, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctxTimeout, "ffmpeg",
		"-f", "concat", "-safe", "0", "-i", listFile.Name(), "-c", "copy", "-y", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg concat failed: %w (%s)", err, strutil.Truncate(string(out), 150))
	}

	merged, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(merged), nil
}
