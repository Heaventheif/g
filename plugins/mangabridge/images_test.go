package mangabridge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var pngSig = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func fakePNG(tag string) []byte { return append(append([]byte{}, pngSig...), []byte(tag)...) }

func okResp(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}
}

func TestFetchImagesWith_ConcurrentAndOrdered(t *testing.T) {
	const n = 12
	const delay = 100 * time.Millisecond
	var cur, peak int32
	fetch := func(ctx context.Context, rawURL string, h http.Header) (*http.Response, error) {
		c := atomic.AddInt32(&cur, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
				break
			}
		}
		time.Sleep(delay)
		atomic.AddInt32(&cur, -1)
		return okResp(fakePNG(rawURL)), nil
	}
	srcs := make([]string, n)
	for i := range srcs {
		srcs[i] = fmt.Sprintf("https://cdn.example/%02d.png", i)
	}

	start := time.Now()
	got := fetchImagesWith(context.Background(), fetch, srcs, "https://example/ch", "", 6, time.Second)
	elapsed := time.Since(start)

	if len(got) != n {
		t.Fatalf("عدد الصور = %d, want %d", len(got), n)
	}
	for i, b := range got {
		if want := fakePNG(srcs[i]); !bytes.Equal(b, want) {
			t.Fatalf("الترتيب مكسور عند %d: %q", i, b[len(pngSig):])
		}
	}
	// تسلسلياً سيكون ≥ n*delay = 1.2s؛ بـ 6 متزامنة ≈ 2*delay.
	if elapsed >= time.Duration(n)*delay/2 {
		t.Fatalf("الزمن %v لا يدل على تزامن (التسلسلي ≥ %v)", elapsed, time.Duration(n)*delay)
	}
	if peak > 6 {
		t.Fatalf("تجاوز حد التزامن: %d", peak)
	}
	if peak < 2 {
		t.Fatalf("لم يحدث تزامن فعلي: peak=%d", peak)
	}
}

func TestFetchImagesWith_DropsFailuresKeepsOrder(t *testing.T) {
	fetch := func(ctx context.Context, rawURL string, h http.Header) (*http.Response, error) {
		if strings.HasSuffix(rawURL, "bad.png") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("<html>not image</html>")), Header: http.Header{}}, nil
		}
		return okResp(fakePNG(rawURL)), nil
	}
	srcs := []string{"https://c/a.png", "https://c/bad.png", "https://c/c.png"}
	got := fetchImagesWith(context.Background(), fetch, srcs, "https://c/", "", 3, 50*time.Millisecond)
	if len(got) != 2 || !bytes.Equal(got[0], fakePNG(srcs[0])) || !bytes.Equal(got[1], fakePNG(srcs[2])) {
		t.Fatalf("النتيجة غير متوقعة: %d صورة", len(got))
	}
}

func TestFetchImagesWith_CookieOnlyToPageHost(t *testing.T) {
	var mu sync.Mutex
	cookies := map[string]string{}
	fetch := func(ctx context.Context, rawURL string, h http.Header) (*http.Response, error) {
		mu.Lock()
		cookies[rawURL] = h.Get("Cookie")
		mu.Unlock()
		if h.Get("Referer") != "https://3asq.online/manga/x/1/" {
			t.Errorf("Referer مفقود: %q", h.Get("Referer"))
		}
		return okResp(fakePNG(rawURL)), nil
	}
	same := "https://3asq.online/a.png"
	sub := "https://cdn.3asq.online/b.png"
	third := "https://evil-cdn.example.net/c.png"
	lookalike := "https://not3asq.online/d.png"
	fetchImagesWith(context.Background(), fetch, []string{same, sub, third, lookalike},
		"https://3asq.online/manga/x/1/", "cf_clearance=SECRET", 4, time.Second)

	if cookies[same] != "cf_clearance=SECRET" || cookies[sub] != "cf_clearance=SECRET" {
		t.Fatalf("الكوكيز يجب أن تصل لمضيف الصفحة ونطاقه الفرعي: %v", cookies)
	}
	if cookies[third] != "" || cookies[lookalike] != "" {
		t.Fatalf("تسرّب كوكيز لمضيف طرف ثالث: %v", cookies)
	}
}

func TestCookieAllowed(t *testing.T) {
	page := "https://3asq.online/manga/x/"
	cases := map[string]bool{
		"https://3asq.online/a.png":     true,
		"https://CDN.3asq.online/a.png": true,
		"https://3asq.online.evil.com/": false,
		"https://x3asq.online/a.png":    false,
		"https://other.com/a.png":       false,
		"::bad::":                       false,
	}
	for src, want := range cases {
		if got := cookieAllowed(src, page); got != want {
			t.Errorf("cookieAllowed(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestFetchImagesWith_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetch := func(ctx context.Context, rawURL string, h http.Header) (*http.Response, error) {
		return nil, ctx.Err()
	}
	got := fetchImagesWith(ctx, fetch, []string{"https://c/a.png", "https://c/b.png"}, "https://c/", "", 2, time.Second)
	if len(got) != 0 {
		t.Fatalf("got %d", len(got))
	}
}
