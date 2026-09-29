package novel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseChapterNumber(t *testing.T) {
	cases := []struct {
		value any
		want  string
		ok    bool
	}{
		{float64(12), "12", true},
		{float64(1.5), "1.5", true},
		{" 2.25 ", "2.25", true},
		{"0", "", false},
		{"1.5x", "", false},
		{"../1", "", false},
	}
	for _, tc := range cases {
		got, ok := parseChapterNumber(tc.value)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseChapterNumber(%v) = (%q, %v), want (%q, %v)", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNovelFullChapterResolutionUsesVisibleExactLabel(t *testing.T) {
	indexHTML := `<a href="/book/chapter-11-reader.html" title="Chapter 1.1 - Reader">Chapter 1.1</a>
		<a href="/book/chapter-12-reader.html" title="Chapter 1.2 - Reader">Chapter 1.2</a>
		<a href="/book/chapter-1-main.html" title="Chapter 1 - Main">Chapter 1</a>
		<a href="https://example.org/chapter-1-evil.html" title="Chapter 1">Chapter 1</a>`
	if got := findNovelFullChapterURL(indexHTML, "1.1"); got != "https://novelfull.com/book/chapter-11-reader.html" {
		t.Fatalf("chapter 1.1 URL = %q", got)
	}
	if got := findNovelFullChapterURL(indexHTML, "1"); got != "https://novelfull.com/book/chapter-1-main.html" {
		t.Fatalf("chapter 1 URL = %q", got)
	}
	if got := findNovelFullChapterURL(indexHTML, "99"); got != "" {
		t.Fatalf("missing chapter URL = %q, want empty", got)
	}
}

func TestNovelSupportedSitesAndChapterURL(t *testing.T) {
	for _, site := range []string{"", "freewebnovel", "Freewebnovel"} {
		got, ok := normalizeNovelSite(site)
		if !ok || got != siteName {
			t.Errorf("normalizeNovelSite(%q) = (%q, %v)", site, got, ok)
		}
	}
	if got, ok := normalizeNovelSite("novelfull"); !ok || got != novelFullSiteName {
		t.Fatalf("normalize NovelFull = (%q, %v)", got, ok)
	}
	if _, ok := normalizeNovelSite("unknown"); ok {
		t.Fatal("unknown source should be rejected")
	}
	if got := buildChapterURL("martial-peak", "1.5"); got != "https://freewebnovel.com/novel/martial-peak/chapter-1.5" {
		t.Fatalf("fractional chapter URL = %q", got)
	}
	result, err := New(http.DefaultClient).handleListSites(nil)
	if err != nil {
		t.Fatal(err)
	}
	sites, ok := result["sites"].([]string)
	if !ok || len(sites) != 2 || sites[0] != siteName || sites[1] != novelFullSiteName {
		t.Fatalf("supported sites = %#v", result["sites"])
	}
}

func TestChallengeDetectionIsSpecific(t *testing.T) {
	if isChallengePage(`<script src="https://cdnjs.cloudflare.com/ajax/libs/x.js"></script>` + strings.Repeat("story text ", 60)) {
		t.Fatal("ordinary page mentioning a Cloudflare asset was treated as a challenge")
	}
	if !isChallengePage(`<title>Just a moment...</title>` + strings.Repeat("challenge ", 60)) {
		t.Fatal("challenge page was not detected")
	}
}

func TestFetchPageHTTPDoesNotRejectNormalCloudflareAsset(t *testing.T) {
	body := `<html><head><script src="https://cdnjs.cloudflare.com/ajax/libs/x.js"></script></head><body>` + strings.Repeat("A normal chapter paragraph. ", 40) + `</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	got := New(server.Client()).fetchPageHTTP(context.Background(), server.URL)
	if got == "" || !strings.Contains(got, "normal chapter") {
		t.Fatal("normal HTML containing a Cloudflare asset should be accepted")
	}
}

func TestFetchPageBrowserLoadsLocalReaderPage(t *testing.T) {
	chromiumPath, err := exec.LookPath("chromium")
	if err != nil {
		chromiumPath, err = exec.LookPath("chromium-browser")
	}
	if err != nil {
		t.Skip("Chromium is not installed")
	}
	t.Setenv("NOVEL_CHROMIUM_PATH", chromiumPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><h1>local chapter reader</h1><div id="chapter-content"><p>Browser fallback fixture.</p></div></body></html>`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pageHTML := fetchPageBrowser(ctx, server.URL)
	if !strings.Contains(pageHTML, "local chapter reader") {
		t.Fatal("browser fallback did not return the local chapter page")
	}
}

func TestNovelFullIndexPagesTargetRequestedChapter(t *testing.T) {
	if got, want := novelFullIndexPages("1.1"), []int{1, 2, 3}; !equalNovelFullPageLists(got, want) {
		t.Fatalf("pages for 1.1 = %v, want %v", got, want)
	}
	if got, want := novelFullIndexPages("254"), []int{6, 7, 5, 8, 4, 1}; !equalNovelFullPageLists(got, want) {
		t.Fatalf("pages for 254 = %v, want %v", got, want)
	}
	if got := novelFullIndexPages("100001"); len(got) != 0 {
		t.Fatalf("pages for out-of-range chapter = %v, want empty", got)
	}
}

func equalNovelFullPageLists(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
