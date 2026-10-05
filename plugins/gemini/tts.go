// tts.go — Groq Orpheus Arabic Saudi primary with Piper Arabic fallback.
// The /gemini/tts path remains backwards-compatible.
package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"sunkenbot/internal/httpx"
	"sunkenbot/plugins/groq"
)

const (
	piperVoice      = "ar_JO-kareem-medium"
	piperDataDir    = "/opt/piper"
	ttsCacheMaxSize = 50
)

var (
	groqPoolMu sync.Mutex
	groqPool   []string
)

func nextGroqVoice() string {
	groqPoolMu.Lock()
	defer groqPoolMu.Unlock()
	if len(groqPool) == 0 {
		groqPool = groq.OrpheusArabicVoices()
		rand.Shuffle(len(groqPool), func(i, j int) { groqPool[i], groqPool[j] = groqPool[j], groqPool[i] })
	}
	voice := groqPool[len(groqPool)-1]
	groqPool = groqPool[:len(groqPool)-1]
	return voice
}

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

func callPiperTTS(ctx context.Context, text string) ([]byte, error) {
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
		"--model", piperVoice,
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

// callGroqThenPiper يجعل Groq Orpheus المزود الأساسي، ولا يستعمل Piper إلا عند فشل Groq.
func callGroqThenPiper(ctx context.Context, text, voice string) ([]byte, string, error) {
	groqVoice := strings.TrimSpace(voice)
	if groqVoice == "" {
		groqVoice = nextGroqVoice()
	}
	groqB64, groqErr := groq.ArabicTTSWithVoice(ctx, &http.Client{Timeout: 45 * time.Second}, text, groqVoice)
	if groqErr == nil {
		audio, err := base64.StdEncoding.DecodeString(groqB64)
		if err == nil && len(audio) > 0 {
			return audio, groqVoice + " (Groq)", nil
		}
		if err == nil {
			groqErr = fmt.Errorf("Groq أعاد صوتاً فارغاً")
		} else {
			groqErr = fmt.Errorf("Groq أعاد صوتاً غير صالح: %w", err)
		}
	}

	piperAudio, piperErr := callPiperTTS(ctx, text)
	if piperErr == nil {
		return piperAudio, piperVoice + " (Piper fallback)", nil
	}
	return nil, "", fmt.Errorf("فشل Groq الأساسي: %v؛ وفشل Piper الاحتياطي: %v", groqErr, piperErr)
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
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "خدمة TTS تدعم النص العربي فقط"}}
	}
	if utf8.RuneCountInString(text) > 3000 {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "النص طويل جداً (3000 حرف كحد أقصى)"}}
	}

	voice := strings.TrimSpace(req.Voice)
	cacheKey := ttsCacheKey(text, voice)
	if cached, cachedVoice, ok := ttsFromCache(cacheKey); ok {
		return TTSResponse{AudioBase64: base64.StdEncoding.EncodeToString(cached), MimeType: "audio/wav", Voice: cachedVoice, Cached: true}, nil
	}

	audio, usedVoice, err := callGroqThenPiper(ctx, text, voice)
	if err != nil {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusServiceUnavailable, Body: map[string]any{"error": truncate(err.Error(), 450)}}
	}
	ttsToCache(cacheKey, audio, usedVoice)
	return TTSResponse{AudioBase64: base64.StdEncoding.EncodeToString(audio), MimeType: "audio/wav", Voice: usedVoice, Cached: false}, nil
}

// GET /gemini/tts/voices
func (s *Service) handleTTSVoices(r *http.Request) (map[string]any, error) {
	return map[string]any{
		"groq_model":           groq.OrpheusArabicModel(),
		"groq_voices":          groq.OrpheusArabicVoices(),
		"default_voice":        groq.OrpheusArabicDefaultVoice(),
		"piper_fallback_model": piperVoice,
		"piper_voices":         []string{piperVoice},
		"pipeline":             "Groq Orpheus Arabic Saudi primary; Piper Arabic fallback",
	}, nil
}

// GET /gemini/tts/cache/stats
func (s *Service) handleTTSCacheStats(r *http.Request) (map[string]any, error) {
	ttsCacheMu.Lock()
	size := len(ttsCacheMap)
	ttsCacheMu.Unlock()
	return map[string]any{
		"cache_size":     size,
		"cache_max_size": ttsCacheMaxSize,
		"pipeline":       "Groq Orpheus Arabic Saudi primary; Piper Arabic fallback",
	}, nil
}
