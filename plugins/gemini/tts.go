// tts.go — Groq-only TTS using Orpheus Arabic Saudi and configured-key rotation.
// The /gemini/tts path remains as a backwards-compatible route name.
package gemini

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/plugins/groq"
)

// ─── Groq voice pool (Orpheus Arabic) ──────────────────────────────
// الأصوات تأتي من groq.OrpheusArabicVoices() — تُعرَّف في groq.go.
// التدوير: pool يُعاد ترتيبه عشوائياً كلما نفد.

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
	v := groqPool[len(groqPool)-1]
	groqPool = groqPool[:len(groqPool)-1]
	return v
}

// ─── TTS Cache (LRU 50 عنصر) ───────────────────────────────────────

const ttsCacheMaxSize = 50

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

// ─── Groq-only TTS ──────────────────────────────────────────────────

var groqTTSClient = &http.Client{Timeout: 45 * time.Second}

// callGroqTTS uses Groq Orpheus only; the Groq client rotates configured API keys.
func (s *Service) callGroqTTS(ctx context.Context, text, voice string) ([]byte, string, error) {
	groqVoice := voice
	if groqVoice == "" {
		groqVoice = nextGroqVoice()
	}
	b64, err := groq.ArabicTTSWithVoice(ctx, groqTTSClient, text, groqVoice)
	if err != nil {
		return nil, "", err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", fmt.Errorf("groq returned invalid audio: %w", err)
	}
	return raw, groqVoice + " (Groq)", nil
}

// ─── HTTP Handlers ──────────────────────────────────────────────────

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
		return TTSResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "text مطلوب"},
		}
	}
	if len(text) > 3000 {
		return TTSResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "النص طويل جداً (3000 حرف كحد أقصى)"},
		}
	}

	voice := strings.TrimSpace(req.Voice)

	// تحقق من الـ Cache
	cacheKey := ttsCacheKey(text, voice)
	if cached, cachedVoice, ok := ttsFromCache(cacheKey); ok {
		return TTSResponse{
			AudioBase64: base64.StdEncoding.EncodeToString(cached),
			MimeType:    "audio/wav",
			Voice:       cachedVoice,
			Cached:      true,
		}, nil
	}

	audio, usedVoice, err := s.callGroqTTS(ctx, text, voice)
	if err != nil {
		return TTSResponse{}, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": "فشل Groq بعد تجربة مفاتيح API المتاحة: " + truncate(err.Error(), 450)},
		}
	}

	// حفظ في الـ Cache
	ttsToCache(cacheKey, audio, usedVoice)

	return TTSResponse{
		AudioBase64: base64.StdEncoding.EncodeToString(audio),
		MimeType:    "audio/wav",
		Voice:       usedVoice,
		Cached:      false,
	}, nil
}

// GET /gemini/tts/voices
func (s *Service) handleTTSVoices(r *http.Request) (map[string]any, error) {
	return map[string]any{
		"groq_model":    groq.OrpheusArabicModel(),
		"groq_voices":   groq.OrpheusArabicVoices(),
		"default_voice": groq.OrpheusArabicDefaultVoice(),
		"pipeline":      "Groq Orpheus Arabic Saudi with configured API-key rotation",
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
		"pipeline":       "Groq Orpheus Arabic Saudi with configured API-key rotation",
	}, nil
}
