// tts.go — TTS pipeline (بدون Gemini):
//   1. Groq (Orpheus Arabic) — الأساسي، مع تدوير أصوات المؤدين
//   2. Edge TTS (Microsoft Neural) — احتياط عربي مجاني بدون مفتاح API
//
// Gemini TTS: محذوف بالكامل (محظور على HuggingFace Spaces).
package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/google/uuid"
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

// ─── Edge TTS Arabic voices (Microsoft Neural) ─────────────────────
// مجاني تماماً — لا يحتاج مفتاح API.
// يُستخدم كاحتياط عند فشل Groq.

var edgeArabicVoices = []string{
	"ar-SA-HamedNeural",   // السعودية — ذكر
	"ar-SA-ZariyahNeural", // السعودية — أنثى
	"ar-EG-ShakirNeural",  // مصر — ذكر
	"ar-EG-SalmaNeural",   // مصر — أنثى
	"ar-DZ-IsmaelNeural",  // الجزائر — ذكر
	"ar-DZ-AminaNeural",   // الجزائر — أنثى
	"ar-MA-JamalNeural",   // المغرب — ذكر
	"ar-MA-MounaNeural",   // المغرب — أنثى
	"ar-IQ-BasselNeural",  // العراق — ذكر
	"ar-IQ-RanaNeural",    // العراق — أنثى
}

var (
	edgePoolMu sync.Mutex
	edgePool   []string
)

func nextEdgeVoice() string {
	edgePoolMu.Lock()
	defer edgePoolMu.Unlock()
	if len(edgePool) == 0 {
		edgePool = append([]string(nil), edgeArabicVoices...)
		rand.Shuffle(len(edgePool), func(i, j int) { edgePool[i], edgePool[j] = edgePool[j], edgePool[i] })
	}
	v := edgePool[len(edgePool)-1]
	edgePool = edgePool[:len(edgePool)-1]
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

// ─── pcmToWav ───────────────────────────────────────────────────────

func pcmToWav(pcm []byte, sampleRate, channels, bitDepth int) []byte {
	byteRate := sampleRate * channels * (bitDepth / 8)
	blockAlign := channels * (bitDepth / 8)
	dataSize := len(pcm)
	wav := make([]byte, 44+dataSize)
	copy(wav[0:4], "RIFF")
	putUint32LE(wav[4:8], uint32(36+dataSize))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	putUint32LE(wav[16:20], 16)
	putUint16LE(wav[20:22], 1)
	putUint16LE(wav[22:24], uint16(channels))
	putUint32LE(wav[24:28], uint32(sampleRate))
	putUint32LE(wav[28:32], uint32(byteRate))
	putUint16LE(wav[32:34], uint16(blockAlign))
	putUint16LE(wav[34:36], uint16(bitDepth))
	copy(wav[36:40], "data")
	putUint32LE(wav[40:44], uint32(dataSize))
	copy(wav[44:], pcm)
	return wav
}

func putUint32LE(b []byte, v uint32) {
	b[0] = byte(v); b[1] = byte(v >> 8); b[2] = byte(v >> 16); b[3] = byte(v >> 24)
}
func putUint16LE(b []byte, v uint16) { b[0] = byte(v); b[1] = byte(v >> 8) }

// ─── Edge TTS (Microsoft Neural) ───────────────────────────────────

const edgeTTSToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"

func escapeSSML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// callEdgeTTS يتصل بـ Microsoft Edge TTS عبر WebSocket ويُعيد WAV.
func callEdgeTTS(ctx context.Context, text, voice string) ([]byte, error) {
	connID := strings.ReplaceAll(uuid.New().String(), "-", "")
	wsURL := fmt.Sprintf(
		"wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1"+
			"?TrustedClientToken=%s&ConnectionId=%s",
		edgeTTSToken, connID,
	)

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	conn, _, _, err := ws.Dial(dialCtx, wsURL)
	if err != nil {
		return nil, fmt.Errorf("edge-tts: فشل الاتصال: %w", err)
	}
	defer conn.Close()

	// ضبط مهلة كاملة للعملية
	if nc, ok := conn.(net.Conn); ok {
		nc.SetDeadline(time.Now().Add(30 * time.Second))
	}

	// رسالة الإعداد
	configMsg := "Content-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n" +
		`{"context":{"synthesis":{"audio":{"metadataoptions":{"sentenceBoundaryEnabled":"false","wordBoundaryEnabled":"false"},"outputFormat":"raw-24khz-16bit-mono-pcm"}}}}`
	if err := wsutil.WriteClientText(conn, []byte(configMsg)); err != nil {
		return nil, fmt.Errorf("edge-tts: فشل إرسال config: %w", err)
	}

	// رسالة SSML
	reqID := strings.ReplaceAll(uuid.New().String(), "-", "")
	ssmlMsg := fmt.Sprintf(
		"X-RequestId:%s\r\nContent-Type:application/ssml+xml\r\nPath:ssml\r\n\r\n"+
			"<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='ar'>"+
			"<voice name='%s'><prosody rate='0%%' pitch='0Hz'>%s</prosody></voice></speak>",
		reqID, voice, escapeSSML(text),
	)
	if err := wsutil.WriteClientText(conn, []byte(ssmlMsg)); err != nil {
		return nil, fmt.Errorf("edge-tts: فشل إرسال SSML: %w", err)
	}

	// استقبال الصوت
	var pcmBuf bytes.Buffer
	for {
		msgs, err := wsutil.ReadServerMessage(conn, nil)
		if err != nil {
			if pcmBuf.Len() > 0 {
				break // انتهت البيانات بشكل طبيعي
			}
			return nil, fmt.Errorf("edge-tts: خطأ في القراءة: %w", err)
		}
		done := false
		for _, msg := range msgs {
			switch msg.OpCode {
			case ws.OpText:
				if strings.Contains(string(msg.Payload), "Path:turn.end") {
					done = true
				}
			case ws.OpBinary:
				// التنسيق: [2 bytes header_len][header_bytes][pcm_bytes]
				data := msg.Payload
				if len(data) < 2 {
					continue
				}
				headerLen := int(data[0])<<8 | int(data[1])
				if len(data) < 2+headerLen {
					continue
				}
				pcmBuf.Write(data[2+headerLen:])
			}
		}
		if done {
			break
		}
	}

	if pcmBuf.Len() == 0 {
		return nil, fmt.Errorf("edge-tts: لم يُستلم صوت")
	}
	// تحويل PCM خام → WAV
	return pcmToWav(pcmBuf.Bytes(), 24000, 1, 16), nil
}

// ─── Pipeline الرئيسي: Groq → Edge TTS ────────────────────────────

var groqFallbackClient = &http.Client{Timeout: 45 * time.Second}

// callTTSWithFallback:
//  1. يجرّب Groq (Orpheus Arabic) بالصوت المحدد مع التدوير
//  2. عند الفشل → يجرّب Edge TTS بصوت عربي عشوائي
func (s *Service) callTTSWithFallback(ctx context.Context, text, voice string) ([]byte, string, error) {
	// ── المرحلة 1: Groq ─────────────────────────────────────────────
	groqVoice := voice
	if groqVoice == "" {
		groqVoice = nextGroqVoice()
	}
	b64, groqErr := groq.ArabicTTSWithVoice(ctx, groqFallbackClient, text, groqVoice)
	if groqErr == nil {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err == nil {
			log.Printf("[tts] ✅ Groq (%s)", groqVoice)
			return raw, groqVoice + " (Groq)", nil
		}
	}
	log.Printf("[tts] Groq فشل (%s) — تجربة Edge TTS...", truncate(groqErr.Error(), 120))

	// ── المرحلة 2: Edge TTS ─────────────────────────────────────────
	edgeVoice := nextEdgeVoice()
	raw, edgeErr := callEdgeTTS(ctx, text, edgeVoice)
	if edgeErr == nil {
		log.Printf("[tts] ✅ Edge TTS (%s) بعد فشل Groq", edgeVoice)
		ttsToCache(ttsCacheKey(text, voice), raw, edgeVoice+" (Edge TTS)")
		return raw, edgeVoice + " (Edge TTS)", nil
	}
	log.Printf("[tts] ❌ Edge TTS فشل أيضاً (%s)", truncate(edgeErr.Error(), 120))

	return nil, "", fmt.Errorf("كل المزودين فشلوا:\nGroq: %s\nEdge TTS: %s",
		truncate(groqErr.Error(), 200), truncate(edgeErr.Error(), 200))
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

	audio, usedVoice, err := s.callTTSWithFallback(ctx, text, voice)
	if err != nil {
		return TTSResponse{}, &httpx.HTTPError{
			Code: http.StatusServiceUnavailable,
			Body: map[string]any{"error": truncate(err.Error(), 500)},
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
		"groq_voices": groq.OrpheusArabicVoices(),
		"edge_voices": edgeArabicVoices,
		"pipeline":    "Groq (Orpheus Arabic) → Edge TTS (Microsoft Neural)",
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
		"pipeline":       "Groq → Edge TTS",
	}, nil
}
