package novel

// fetch.go — جلب الصفحات: HTTP والمتصفح وكشف التحدّي.

import (
	"context"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"sunkenbot/internal/browser"
)

// ─── User agents ─────────────────────────────────────────────────

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:126.0) Gecko/20100101 Firefox/126.0",
}

func randomUA() string { return userAgents[rand.Intn(len(userAgents))] }

func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", randomUA())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
}

// ─── جلب الصفحة عبر HTTP ────────────────────────────────────────

func isChallengePage(pageHTML string) bool {
	sample := strings.ToLower(pageHTML)
	if len(sample) > 6000 {
		sample = sample[:6000]
	}
	for _, marker := range []string{"just a moment", "checking your browser", "cf-chl-", "challenge-platform", "verify you are human", "attention required | cloudflare"} {
		if strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}

func (s *Service) fetchPageHTTP(ctx context.Context, url string) string {
	const retries = 2
	for attempt := 0; attempt <= retries; attempt++ {
		ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(ctxTimeout, http.MethodGet, url, nil)
		if err == nil {
			setBrowserHeaders(req)
			resp, err := s.client.Do(req)
			if err == nil {
				body := readAllLimited(resp.Body, 5*1024*1024)
				resp.Body.Close()
				if resp.StatusCode == 200 && len(body) > 500 {
					if isChallengePage(body) {
						log.Printf("[novel] Cloudflare على %s", url)
						cancel()
						break
					}
					cancel()
					return body
				}
			}
		}
		cancel()
		time.Sleep(time.Second)
	}
	return ""
}

// readAllLimited يقرأ حتى limit بايت فقط (الزائد يُهمل).
func readAllLimited(r io.Reader, limit int64) string {
	b, _ := io.ReadAll(io.LimitReader(r, limit))
	return string(b)
}

// fetchPageBrowser يقابل fetch_page_playwright في بايثون. يستخدم الآن
// internal/browser (chromedp حقيقي، بعد أن أصبح متاحاً في هذه البيئة)
// بدل التخطي الصامت السابق: يفتح جلسة Chrome بلا واجهة عبر browser.New
// (نفس stealth script + user agent عشوائي المستخدمين في pinterest/manga_bridge)،
// ينتقل إلى url، ينتظر زوال أي تحدي Cloudflare، ثم يُعيد outerHTML كاملاً
// لتغذية نفس extract()/extractTitle() الموجودين أعلاه — لا حاجة لتكرار
// منطق التحليل، فقط طريقة الجلب تغيّرت من HTTP بسيط إلى متصفح حقيقي.
func fetchPageBrowser(ctx context.Context, url string) string {
	bctx, cancel, err := browser.New(ctx, browser.Options{ExecPathEnv: "NOVEL_CHROMIUM_PATH"})
	if err != nil {
		log.Printf("[novel] [Browser] تعذّر تشغيل Chromium: %v", err)
		return ""
	}
	defer cancel()

	navCtx, navCancel := context.WithTimeout(bctx, 45*time.Second)
	defer navCancel()

	var htmlContent string
	if err := chromedp.Run(navCtx,
		chromedp.Navigate(url),
		chromedp.WaitNotPresent(`#challenge-form`, chromedp.ByID),
		chromedp.WaitNotPresent(`#cf-challenge-running`, chromedp.ByID),
		chromedp.WaitNotPresent(`.cf-browser-verification`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	); err != nil {
		log.Printf("[novel] [Browser] فشل التنقل إلى %s: %v", url, err)
		return ""
	}

	if isChallengePage(htmlContent) {
		log.Printf("[novel] [Browser] Cloudflare لا يزال ظاهراً بعد الانتظار على %s", url)
		return ""
	}

	return htmlContent
}
