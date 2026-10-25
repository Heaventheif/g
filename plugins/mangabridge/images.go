package mangabridge

// images.go — تنزيل صور الفصل والتحقق منها.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"sunkenbot/internal/netguard"
)

const (
	// imageFetchConcurrency: عدد طلبات الصور المتزامنة داخل الفصل الواحد.
	imageFetchConcurrency = 6
	// imageFetchTimeout: مهلة المحاولة الواحدة (كانت 20s تسلسلياً).
	imageFetchTimeout = 10 * time.Second
	imageFetchRetries = 2
)

// imageFetchFunc يجلب رابط صورة بالترويسات المعطاة. الإنتاج يمرّ عبر netguard
// (روابط الصور مستخرجة من صفحة خارجية ⇒ غير موثوقة)؛ الاختبارات تحقن بديلاً
// لأن netguard يرفض عناوين loopback التي يستخدمها httptest.
type imageFetchFunc func(ctx context.Context, rawURL string, header http.Header) (*http.Response, error)

// fetchImages يقابل حلقة تحميل الصور في scrapeChapter (manga_scraper.js
// السابق): يطلب كل رابط بـ Referer=chapterURL ومحاولتين عند الفشل، بنفس
// الـ User-Agent حتى يبقى الخادم البعيد يرى نفس الهوية التي اجتازت تحدي
// Cloudflare. الكوكيز تُرسل فقط لمضيف الصفحة نفسه (أو نطاقاته الفرعية) — لا
// لأي CDN طرف ثالث (راجع cookieAllowed).
func fetchImages(ctx context.Context, srcs []string, referer, cookieHeader string) [][]byte {
	client := &http.Client{} // المهلة لكل محاولة عبر context
	fetch := func(ctx context.Context, rawURL string, header http.Header) (*http.Response, error) {
		return netguard.SafeFetchStreamWithHeader(ctx, client, rawURL, header)
	}
	return fetchImagesWith(ctx, fetch, srcs, referer, cookieHeader, imageFetchConcurrency, imageFetchTimeout)
}

// fetchImagesWith هو التنفيذ الفعلي: متزامن بحد concurrency، ويحافظ على ترتيب
// الصفحات (كل نتيجة تُكتب في فهرس رابطها). الصور الفاشلة تُسقط وتُرصّ الباقية.
func fetchImagesWith(ctx context.Context, fetch imageFetchFunc, srcs []string, referer, cookieHeader string, concurrency int, timeout time.Duration) [][]byte {
	if len(srcs) > maxMangaImages {
		srcs = srcs[:maxMangaImages]
	}
	if concurrency < 1 {
		concurrency = 1
	}
	results := make([][]byte, len(srcs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, src := range srcs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fetchOneImage(ctx, fetch, src, referer, cookieHeader, timeout)
		}()
	}
	wg.Wait()

	buffers := make([][]byte, 0, len(srcs))
	for _, b := range results {
		if b != nil {
			buffers = append(buffers, b)
		}
	}
	return buffers
}

func fetchOneImage(ctx context.Context, fetch imageFetchFunc, src, referer, cookieHeader string, timeout time.Duration) []byte {
	header := http.Header{}
	header.Set("Referer", referer)
	header.Set("User-Agent", mangaUserAgent)
	if cookieHeader != "" && cookieAllowed(src, referer) {
		header.Set("Cookie", cookieHeader)
	}
	for attempt := range imageFetchRetries {
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := fetch(reqCtx, src, header)
		if err == nil {
			var b []byte
			var readErr error
			if resp.StatusCode == http.StatusOK {
				b, readErr = io.ReadAll(io.LimitReader(resp.Body, maxMangaImageBytes+1))
			}
			resp.Body.Close()
			cancel()
			if resp.StatusCode == http.StatusOK && readErr == nil && len(b) <= maxMangaImageBytes && isImagePayload(b) {
				return b
			}
		} else {
			cancel()
		}
		if attempt < imageFetchRetries-1 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
	return nil
}

// cookieAllowed: كوكيز الجلسة (وفيها cf_clearance) تُرسل فقط إن كان مضيف الصورة
// هو مضيف الصفحة نفسه أو نطاقاً فرعياً منه. بدون قائمة public-suffix لا نوسّع
// أكثر من ذلك عمداً (الأمان أهم من تغطية CDN شقيق على نطاق أب مشترك).
func cookieAllowed(srcURL, pageURL string) bool {
	su, err1 := url.Parse(srcURL)
	pu, err2 := url.Parse(pageURL)
	if err1 != nil || err2 != nil {
		return false
	}
	sh, ph := strings.ToLower(su.Hostname()), strings.ToLower(pu.Hostname())
	if sh == "" || ph == "" {
		return false
	}
	return sh == ph || strings.HasSuffix(sh, "."+ph)
}

func imageFileExtension(data []byte) string {
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ".webp"
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" && (string(data[8:12]) == "avif" || string(data[8:12]) == "avis") {
		return ".avif"
	}
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tif"
	default:
		return ".img"
	}
}

func isImagePayload(data []byte) bool {
	return len(data) > 0 && imageFileExtension(data) != ".img"
}
