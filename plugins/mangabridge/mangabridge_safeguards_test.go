package mangabridge

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageFileExtensionUsesDetectedImageType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0x00}, ".jpg"},
		{"png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, ".png"},
		{"gif", []byte("GIF89a"), ".gif"},
		{"webp", []byte("RIFF0000WEBP"), ".webp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageFileExtension(tc.data); got != tc.want {
				t.Fatalf("imageFileExtension = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageExtractionScriptNormalizesAndBoundsURLs(t *testing.T) {
	if !strings.Contains(extractImgSrcsJS, "new URL(src.trim(), document.baseURI)") {
		t.Fatal("relative reader image URLs should be normalized against the page")
	}
	if !strings.Contains(extractImgSrcsJS, "seen.has(absolute.href)") || !strings.Contains(extractImgSrcsJS, "out.length >= 300") {
		t.Fatal("image extraction should deduplicate and cap the page list")
	}
	if !strings.Contains(extractImgSrcsJS, "absolute.protocol !== 'https:'") {
		t.Fatal("reader image extraction should not return non-HTTPS URLs")
	}
}

func TestImageRouteRejectsNonGeneratedJobIDs(t *testing.T) {
	req := httptest.NewRequest("GET", "/manga-bridge/jobs/../../etc/0", nil)
	req.SetPathValue("job_id", "../../etc")
	req.SetPathValue("idx", "0")
	res := httptest.NewRecorder()
	New().handleGetJobImage(res, req)
	if res.Code != 400 {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}
