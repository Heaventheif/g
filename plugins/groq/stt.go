package groq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/netguard"
)

const whisperModel = "whisper-large-v3"

// handleSTT is the standalone speech-to-text endpoint used by the stt command.
// It accepts either an attachment URL or base64 audio and returns only the
// transcription; unlike /groq it does not ask a chat model to summarize it.
func (s *Service) handleSTT(r *http.Request) (map[string]any, error) {
	ctx := r.Context()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": truncate(err.Error(), 200)}}
	}

	att := parseAttachment(body["attachment"])
	if att == nil {
		att = &attachment{
			Kind:        "audio",
			URL:         stringOr(body["url"], ""),
			Base64:      stringOr(body["base64"], ""),
			ContentType: stringOr(body["contentType"], ""),
		}
	}
	if att.URL == "" && att.Base64 == "" {
		return nil, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "أرسل ملفاً صوتياً أو رابط audio"}}
	}
	if att.Kind != "" && att.Kind != "audio" {
		return nil, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "المرفق يجب أن يكون صوتاً"}}
	}

	var raw []byte
	var err error
	if att.URL != "" {
		raw, err = netguard.SafeFetch(ctx, s.dlClient, att.URL, maxSTTBytes)
		if err != nil {
			err = fmt.Errorf("رابط الصوت مرفوض أو تعذّر جلبه: %w", err)
		}
	} else {
		raw, err = base64.StdEncoding.DecodeString(att.Base64)
		if err == nil && len(raw) > maxSTTBytes {
			err = fmt.Errorf("حجم الصوت يتجاوز 25MB")
		}
	}
	if err != nil {
		return nil, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]any{"error": "تعذّر قراءة الملف الصوتي: " + truncate(err.Error(), 180)}}
	}

	language := stringOr(body["language"], "ar")
	responseFormat := stringOr(body["response_format"], "text")
	text, err := s.transcribeAudio(ctx, raw, guessMime(att.URL, raw), language, responseFormat)
	if err != nil {
		return nil, &httpx.HTTPError{Code: http.StatusServiceUnavailable, Body: map[string]any{"error": "فشل Groq STT بعد تجربة مفاتيح API المتاحة: " + truncate(err.Error(), 300)}}
	}
	return map[string]any{"text": text, "model": whisperModel, "provider": "groq-whisper"}, nil
}

const maxSTTBytes = 25 * 1024 * 1024

func (s *Service) transcribeAudio(ctx context.Context, audioRaw []byte, mime, language, responseFormat string) (string, error) {
	if len(audioRaw) == 0 {
		return "", fmt.Errorf("EMPTY_AUDIO")
	}
	if len(audioRaw) > maxSTTBytes {
		return "", fmt.Errorf("audio exceeds 25MB")
	}
	extMap := map[string]string{
		"audio/mp3": "mp3", "audio/mpeg": "mp3", "audio/mp4": "m4a", "audio/m4a": "m4a",
		"audio/ogg": "ogg", "audio/wav": "wav", "audio/webm": "webm", "audio/flac": "flac",
		"video/mp4": "mp4", "video/webm": "webm",
	}
	ext := extMap[mime]
	if ext == "" {
		ext = "mp3"
	}
	if responseFormat != "json" && responseFormat != "verbose_json" && responseFormat != "text" {
		responseFormat = "text"
	}

	mgr := groqKeys()
	if mgr.Empty() {
		return "", fmt.Errorf("NO_GROQ_KEY")
	}
	var transcription string
	err := mgr.Do(func(key string) (bool, error) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("file", "audio."+ext)
		if err != nil {
			return false, err
		}
		if _, err := part.Write(audioRaw); err != nil {
			return false, err
		}
		_ = mw.WriteField("model", whisperModel)
		if language != "" {
			_ = mw.WriteField("language", language)
		}
		_ = mw.WriteField("response_format", responseFormat)
		if err := mw.Close(); err != nil {
			return false, err
		}

		ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctxTimeout, http.MethodPost,
			"https://api.groq.com/openai/v1/audio/transcriptions", &buf)
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := s.client.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, err
		}
		if resp.StatusCode >= 400 {
			return resp.StatusCode == http.StatusTooManyRequests,
				fmt.Errorf("groq STT status %d: %s", resp.StatusCode, truncate(string(respBody), 250))
		}
		if responseFormat == "text" {
			transcription = strings.TrimSpace(string(respBody))
		} else {
			var decoded struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(respBody, &decoded); err != nil {
				return false, err
			}
			transcription = strings.TrimSpace(decoded.Text)
		}
		if transcription == "" {
			return false, fmt.Errorf("EMPTY_TRANSCRIPTION")
		}
		return false, nil
	})
	return transcription, err
}
