// tts.go — Piper Arabic-only TTS. The /gemini/tts path remains backwards-compatible.
package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"

	"sunkenbot/internal/httpx"
)

const (
	piperVoice      = "ar_JO-kareem-medium"
	piperDataDir    = "/opt/piper"
	ttsCacheMaxSize = 50
)

// TTS Cache (LRU 50 عنصر)
type ttsCacheEntry struct {
	wav   []byte
	voice string
}

var (
	ttsCacheMu  sync.Mutex
	ttsCacheMap = make(map[string]ttsCacheEntry, ttsCacheMaxSize)
	ttsCacheOrd []string
)

func ttsCacheKey(text, voice string) string {
	h := sha256.Sum256([]byte(text + "\x00" + voice))
	return base64.RawStdEncoding.EncodeToString(h[:16])
}

func ttsFromCache(key string) ([]byte, string, bool) {
	ttsCacheMu.Lock()
	defer ttsCacheMu.Unlock()
	if e, ok := ttsCacheMap[key]; ok {
		return e.wav, e.voice, true
	}
	return nil, "", false
}

func ttsToCache(key string, wav []byte, voice string) {
	ttsCacheMu.Lock()
	defer ttsCacheMu.Unlock()
	if _, exists := ttsCacheMap[key]; exists {
		return
	}
	if len(ttsCacheOrd) >= ttsCacheMaxSize {
		oldest := ttsCacheOrd[0]
		ttsCacheOrd = ttsCacheOrd[1:]
		delete(ttsCacheMap, oldest)
	}
	ttsCacheMap[key] = ttsCacheEntry{wav: wav, voice: voice}
	ttsCacheOrd = append(ttsCacheOrd, key)
}

func hasArabicText(text string) bool {
	for _, r := range text {
		if (r >= '\u0600' && r <= '\u06ff') || (r >= '\u0750' && r <= '\u077f') {
			return true
		}
	}
	return false
}

func normalizePiperVoice(voice string) (string, error) {
	voice = strings.TrimSpace(voice)
	if voice == "" || voice == "kareem" || voice == piperVoice {
		return piperVoice, nil
	}
	return "", fmt.Errorf("الصوت غير مدعوم؛ Piper العربي المتاح هو %s", piperVoice)
}

func callPiperTTS(ctx context.Context, text, voice string) ([]byte, error) {
	voice, err := normalizePiperVoice(voice)
	if err != nil {
		return nil, err
	}
	out, err := os.CreateTemp("", "sunkenbot-piper-*.wav")
	if err != nil {
		return nil, fmt.Errorf("إنشاء ملف Piper مؤقت: %w", err)
	}
	outPath := out.Name()
	if err := out.Close(); err != nil {
		os.Remove(outPath)
		return nil, fmt.Errorf("فتح ملف Piper المؤقت: %w", err)
	}
	defer os.Remove(outPath)

	cmd := exec.CommandContext(ctx, "python3", "-m", "piper",
		"--data-dir", piperDataDir,
		"--model", voice,
		"--output_file", outPath,
	)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("Piper TTS فشل: %s", truncate(detail, 350))
	}
	audio, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("قراءة صوت Piper: %w", err)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("Piper أعاد ملفاً صوتياً فارغاً")
	}
	return audio, nil
}

// HTTP Handlers

type TTSRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

type TTSResponse struct {
	AudioBase64 string `json:"audio_base64"`
	MimeType    string `json:"mime_type"`
	Voice       string `json:"voice"`
	Cached      bool   `json:"cached"`
}

func (s *Service) handleTTS(ctx context.Context, req TTSRequest) (TTSResponse, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "text مطلوب"}}
	}
	if !hasArabicText(text) {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "Piper TTS يدعم النص العربي فقط"}}
	}
	if utf8.RuneCountInString(text) > 3000 {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "النص طويل جداً (3000 حرف كحد أقصى)"}}
	}

	voice, err := normalizePiperVoice(req.Voice)
	if err != nil {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": err.Error()}}
	}
	cacheKey := ttsCacheKey(text, voice)
	if cached, cachedVoice, ok := ttsFromCache(cacheKey); ok {
		return TTSResponse{AudioBase64: base64.StdEncoding.EncodeToString(cached), MimeType: "audio/wav", Voice: cachedVoice, Cached: true}, nil
	}

	audio, err := callPiperTTS(ctx, text, voice)
	if err != nil {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusServiceUnavailable, Body: map[string]any{"error": truncate(err.Error(), 450)}}
	}
	ttsToCache(cacheKey, audio, voice)
	return TTSResponse{AudioBase64: base64.StdEncoding.EncodeToString(audio), MimeType: "audio/wav", Voice: voice, Cached: false}, nil
}

// GET /gemini/tts/voices
func (s *Service) handleTTSVoices(r *http.Request) (map[string]any, error) {
	return map[string]any{
		"piper_model":   piperVoice,
		"piper_voices":  []string{piperVoice},
		"default_voice": piperVoice,
		"pipeline":      "Piper Arabic-only local TTS",
	}, nil
}

// GET /gemini/tts/cache/stats
func (s *Service) handleTTSCacheStats(r *http.Request) (map[string]any, error) {
	ttsCacheMu.Lock()
	size := len(ttsCacheMap)
	ttsCacheMu.Unlock()
	return map[string]any{"cache_size": size, "cache_max_size": ttsCacheMaxSize, "pipeline": "Piper Arabic-only local TTS"}, nil
}
