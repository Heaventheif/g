package groq

// attachment.go — المرفقات: تحليل وتنزيل ومعالجة الفيديو.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"sunkenbot/internal/netguard"
	"sunkenbot/internal/session"
	"sunkenbot/internal/strutil"
)

// attachment represents incoming file payloads (images, audio, video).
type attachment struct {
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Base64      string `json:"base64"`
	ContentType string `json:"contentType"`
	Model       string `json:"model"`
}

// parseAttachment safely extracts attachment data from raw JSON input.
func parseAttachment(raw any) *attachment {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &attachment{
		Kind:        strutil.StringOr(obj["kind"], ""),
		URL:         strutil.StringOr(obj["url"], ""),
		Base64:      strutil.StringOr(obj["base64"], ""),
		ContentType: strutil.StringOr(obj["contentType"], ""),
		Model:       strutil.StringOr(obj["model"], ""),
	}
}

const (
	maxAttachmentBytes   = 20 * 1024 * 1024 // 20MB — حد Groq لجلب الروابط (URL)
	maxBase64VisionBytes = 4 * 1024 * 1024  // 4MB  — حد Groq لصور base64 (يُعيد 413 إذا تجاوز)
)

// fetchBase64 securely downloads an attachment from a URL and converts it to Base64.
func (s *Service) fetchBase64(ctx context.Context, url string) ([]byte, string, error) {
	raw, err := netguard.SafeFetch(ctx, s.dlClient, url, maxAttachmentBytes)
	if err != nil {
		return nil, "", fmt.Errorf("رابط المرفق مرفوض أو تعذّر جلبه: %w", err)
	}
	return raw, base64.StdEncoding.EncodeToString(raw), nil
}

// guessMime determines the MIME type of a file based on its file extension or raw header bytes.
func guessMime(url string, raw []byte) string {
	low := strings.ToLower(strings.SplitN(url, "?", 2)[0])
	switch {
	case strings.HasSuffix(low, ".png"):
		return "image/png"
	case strings.HasSuffix(low, ".gif"):
		return "image/gif"
	case strings.HasSuffix(low, ".webp"):
		return "image/webp"
	case strings.HasSuffix(low, ".mp3"):
		return "audio/mp3"
	case strings.HasSuffix(low, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(low, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(low, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(low, ".mp4"):
		return "video/mp4"
	}
	switch {
	case len(raw) >= 4 && bytes.Equal(raw[:4], []byte{0x89, 'P', 'N', 'G'}):
		return "image/png"
	case len(raw) >= 3 && string(raw[:3]) == "GIF":
		return "image/gif"
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xD8:
		return "image/jpeg"
	case len(raw) >= 4 && string(raw[:4]) == "RIFF":
		return "audio/wav"
	case len(raw) >= 3 && string(raw[:3]) == "ID3":
		return "audio/mp3"
	}
	return "image/jpeg"
}

// processVideo extracts the first frame of a video using FFmpeg and passes it to the vision model.
func (s *Service) processVideo(ctx context.Context, url, prompt string, messages []session.Message) (string, error) {
	raw, _, err := s.fetchBase64(ctx, url)
	if err != nil {
		return videoFailMessage(err), nil
	}

	vidFile, err := os.CreateTemp("", "sunkenbot-*.mp4")
	if err != nil {
		return videoFailMessage(err), nil
	}
	vidPath := vidFile.Name()
	defer os.Remove(vidPath)
	if _, err := vidFile.Write(raw); err != nil {
		vidFile.Close()
		return videoFailMessage(err), nil
	}
	vidFile.Close()

	framePath := strings.TrimSuffix(vidPath, ".mp4") + "_frame.jpg"
	defer os.Remove(framePath)

	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctxTimeout, "ffmpeg",
		"-i", vidPath, "-ss", "00:00:01", "-vframes", "1", "-q:v", "2", framePath, "-y")
	if err := cmd.Run(); err != nil {
		return videoFailMessage(fmt.Errorf("ffmpeg failed: %w", err)), nil
	}

	frameRaw, err := os.ReadFile(framePath)
	if err != nil {
		return videoFailMessage(err), nil
	}

	imgURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(frameRaw)
	reply, err := s.groqVision(ctx, messages, imgURL, "")
	if err != nil {
		return videoFailMessage(err), nil
	}
	return fmt.Sprintf("🎬 تحليل الفيديو (الإطار الأول):\n%s", reply), nil
}

// videoFailMessage returns a standardized fallback warning when video processing fails.
func videoFailMessage(err error) string {
	return fmt.Sprintf("⚠️ تعذّر تحليل الفيديو (%s). يمكنك أخذ screenshot وإرساله كصورة.", strutil.Truncate(err.Error(), 60))
}
