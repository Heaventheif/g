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
	"sunkenbot/internal/strutil"
	sharedtts "sunkenbot/internal/tts"
)

const (
	piperVoice      = "ar_JO-kareem-medium"
	piperDataDir    = "/opt/piper"
	ttsCacheMaxSize = 50
)

// ttsState حالة TTS كحقل في Service (بدل متغيرات حزمة عامة): كاش LRU بسعة
// ttsCacheMaxSize + مجمّع أصوات Groq العشوائي. القيمة الصفرية صالحة للاستعمال
// (الخريطة تُنشأ عند أول كتابة) لأن الاختبارات تبني &Service{} مباشرة.
type ttsState struct {
	mu        sync.Mutex
	cache     map[string]ttsCacheEntry
	order     []string
	voicePool []string
}

// TTS Cache (LRU 50 عنصر)
type ttsCacheEntry struct {
	wav   []byte
	voice string
}

// nextVoice يعيد صوتاً من مجمّع مُخلَّط، ويعيد تعبئته عند نفاده.
func (t *ttsState) nextVoice() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.voicePool) == 0 {
		t.voicePool = sharedtts.OrpheusArabicVoices()
		rand.Shuffle(len(t.voicePool), func(i, j int) { t.voicePool[i], t.voicePool[j] = t.voicePool[j], t.voicePool[i] })
	}
	voice := t.voicePool[len(t.voicePool)-1]
	t.voicePool = t.voicePool[:len(t.voicePool)-1]
	return voice
}

func ttsCacheKey(text, voice string) string {
	h := sha256.Sum256([]byte(text + "\x00" + voice))
	return base64.RawStdEncoding.EncodeToString(h[:16])
}

func (t *ttsState) fromCache(key string) ([]byte, string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.cache[key]; ok {
		return e.wav, e.voice, true
	}
	return nil, "", false
}

func (t *ttsState) toCache(key string, wav []byte, voice string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cache == nil {
		t.cache = make(map[string]ttsCacheEntry, ttsCacheMaxSize)
	}
	if _, exists := t.cache[key]; exists {
		return
	}
	if len(t.order) >= ttsCacheMaxSize {
		oldest := t.order[0]
		t.order = t.order[1:]
		delete(t.cache, oldest)
	}
	t.cache[key] = ttsCacheEntry{wav: wav, voice: voice}
	t.order = append(t.order, key)
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

	// ملف Piper التنفيذي المستقل (C++، بلا Python) — يُثبَّت في Dockerfile.
	cmd := exec.CommandContext(ctx, "piper",
		"--model", piperDataDir+"/"+piperVoice+".onnx",
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
		return nil, fmt.Errorf("Piper TTS فشل: %s", strutil.Truncate(detail, 350))
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
func (s *Service) callGroqThenPiper(ctx context.Context, text, voice string) ([]byte, string, error) {
	groqVoice := strings.TrimSpace(voice)
	if groqVoice == "" {
		groqVoice = s.tts.nextVoice()
	}
	groqB64, groqErr := sharedtts.ArabicTTSWithVoice(ctx, &http.Client{Timeout: 45 * time.Second}, text, groqVoice)
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
	if cached, cachedVoice, ok := s.tts.fromCache(cacheKey); ok {
		return TTSResponse{AudioBase64: base64.StdEncoding.EncodeToString(cached), MimeType: "audio/wav", Voice: cachedVoice, Cached: true}, nil
	}

	audio, usedVoice, err := s.callGroqThenPiper(ctx, text, voice)
	if err != nil {
		return TTSResponse{}, &httpx.HTTPError{Code: http.StatusServiceUnavailable, Body: map[string]any{"error": strutil.Truncate(err.Error(), 450)}}
	}
	s.tts.toCache(cacheKey, audio, usedVoice)
	return TTSResponse{AudioBase64: base64.StdEncoding.EncodeToString(audio), MimeType: "audio/wav", Voice: usedVoice, Cached: false}, nil
}

// GET /gemini/tts/voices
func (s *Service) handleTTSVoices(r *http.Request) (map[string]any, error) {
	return map[string]any{
		"groq_model":           sharedtts.OrpheusArabicModel(),
		"groq_voices":          sharedtts.OrpheusArabicVoices(),
		"default_voice":        sharedtts.OrpheusArabicDefaultVoice(),
		"piper_fallback_model": piperVoice,
		"piper_voices":         []string{piperVoice},
		"pipeline":             "Groq Orpheus Arabic Saudi primary; Piper Arabic fallback",
	}, nil
}
