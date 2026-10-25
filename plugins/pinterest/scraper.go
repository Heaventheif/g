package pinterest

// scraper.go — كشط Pinterest عبر المتصفح.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"sunkenbot/internal/browser"
)

// ─── تشغيل الكشط الفعلي عبر chromedp (CDP مباشرة، لا Node) ─────────

const (
	pinterestBaseURL      = "https://www.pinterest.com"
	pinterestViewportW    = 1280
	pinterestViewportH    = 900
	pinterestNavTimeout   = 30 * time.Second
	pinterestNavRetries   = 2
	pinterestChallengeMax = 20 * time.Second
	pinterestScrollWait   = 1200 * time.Millisecond
	pinterestPageGapWait  = 1000 * time.Millisecond
)

var pinterestChallengeMarkers = []string{
	"just a moment",
	"attention required",
	"checking your browser",
	"ddos protection",
	"verify you are human",
}

// extractPinsJS: نفس EXTRACT_JS بالضبط من pinterest_scraper.js السابق،
// مُنفَّذة داخل الصفحة عبر chromedp.Evaluate (المكافئ لِـ page.evaluate).
const extractPinsJS = `
(() => {
    const cards = document.querySelectorAll('.ADXRXN');
    const out = [];
    const seen = new Set();
    for (const c of cards) {
        const a = c.querySelector('a[href*="/pin/"]');
        if (!a) continue;
        const m = (a.getAttribute('href') || '').match(/\/pin\/(\d+)/);
        if (!m) continue;
        const id = m[1];
        if (seen.has(id)) continue;
        seen.add(id);
        const img = c.querySelector('img');
        const h = c.querySelector('h1, h2, h3, [class*="title"]');
        out.push({
            id,
            url: a.href,
            title: h ? h.textContent.trim() : '',
            image_src: img ? (img.getAttribute('src') || '') : '',
            image_srcset: img ? (img.getAttribute('srcset') || '') : ''
        });
    }
    return out;
})()
`

func (s *Service) runScraper(ctx context.Context, req pinterestRequest) ([]imageResult, error) {
	// حارس التزامن: يحد عدد نسخ Chromium المتشغّلة معاً.
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	pages := max((req.Limit+24)/25, 1)

	var listingURL string
	if req.Username != "" && req.BoardSlug != "" {
		listingURL = fmt.Sprintf("%s/%s/%s/", pinterestBaseURL, req.Username, req.BoardSlug)
	} else {
		listingURL = fmt.Sprintf("%s/search/pins/?q=%s&rs=typed", pinterestBaseURL, url.QueryEscape(req.Query))
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	bctx, bcancel, err := browser.New(ctxTimeout, browser.Options{
		WindowWidth:  pinterestViewportW,
		WindowHeight: pinterestViewportH,
		ExecPathEnv:  "PINTEREST_CHROMIUM_PATH",
	})
	if err != nil {
		return nil, err
	}
	defer bcancel()

	rawPins, err := scrapePinterestListing(bctx, listingURL, pages, 6)
	if err != nil {
		return nil, err
	}

	images := make([]imageResult, 0, len(rawPins))
	for _, p := range rawPins {
		if img := pinFromRaw(p, req.Quality); img != nil {
			images = append(images, *img)
		}
	}
	return images, nil
}

// scrapePinterestListing يقابل scrapeListing في pinterest_scraper.js
// السابق: يمر على صفحات ?page=1..pages، يستخرج pins جديدة من كل صفحة،
// ويتوقف مبكراً لو صفحة كاملة لم تُضِف أي pin جديدة (نفس addedAny في
// النسخة السابقة).
func scrapePinterestListing(ctx context.Context, listingURL string, pages, maxScrolls int) ([]rawPin, error) {
	var allPins []rawPin
	seen := map[string]bool{}

	for i := 1; i <= pages; i++ {
		sep := "?"
		if strings.Contains(listingURL, "?") {
			sep = "&"
		}
		pageURL := fmt.Sprintf("%s%spage=%d", listingURL, sep, i)

		pins, err := scrapePinterestPage(ctx, pageURL, maxScrolls)
		if err != nil {
			// فشل صفحة واحدة لا يجب أن يُسقط ما جُمع من صفحات سابقة —
			// لكن لو كانت هذه الصفحة الأولى (لا نتائج بعد إطلاقاً)، نُعيد
			// الخطأ فعلياً حتى لا يظهر "نجاح" بصفر صور بلا تفسير.
			if i == 1 && len(allPins) == 0 {
				return nil, err
			}
			break
		}

		addedAny := false
		for _, p := range pins {
			if p.ID == "" || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			allPins = append(allPins, p)
			addedAny = true
		}
		if !addedAny {
			break
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(pinterestPageGapWait)); err != nil {
			break
		}
	}
	return allPins, nil
}

// scrapePinterestPage يقابل scrapeFullURL في pinterest_scraper.js
// السابق: تنقّل مع إعادة محاولة، انتظار زوال أي تحدي Cloudflare، انتظار
// ظهور أول pin، ثم حلقة سكرول+استخراج بحد maxScrolls.
func scrapePinterestPage(ctx context.Context, pageURL string, maxScrolls int) ([]rawPin, error) {
	if err := navigateWithRetry(ctx, pageURL, pinterestNavTimeout, pinterestNavRetries); err != nil {
		return nil, err
	}
	waitForChallengeToClear(ctx, pinterestChallengeMax, pinterestChallengeMarkers)

	// انتظار ظهور أول pin — فشل هذا الانتظار لا يوقف الكشط (قد تظهر
	// النتائج لاحقاً أثناء السكرول)، تماماً كما في try/catch الأصلي.
	waitCtx, waitCancel := context.WithTimeout(ctx, 15*time.Second)
	_ = chromedp.Run(waitCtx, chromedp.WaitVisible(`.ADXRXN, a[href*="/pin/"]`, chromedp.ByQuery))
	waitCancel()

	seen := map[string]bool{}
	var pins []rawPin

	for range maxScrolls {
		var raw []rawPin
		if err := chromedp.Run(ctx, chromedp.Evaluate(extractPinsJS, &raw)); err != nil {
			return pins, err
		}
		for _, d := range raw {
			if d.ID == "" || seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			pins = append(pins, d)
		}
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`window.scrollBy(0, window.innerHeight * 0.9)`, nil),
			chromedp.Sleep(pinterestScrollWait),
		); err != nil {
			return pins, err
		}
	}
	return pins, nil
}

// navigateWithRetry يقابل gotoWithRetry في النسخة السابقة: يحاول
// التنقّل حتى retries+1 مرة، مع فاصل عشوائي بسيط بين المحاولات عند
// الفشل.
func navigateWithRetry(ctx context.Context, u string, timeout time.Duration, retries int) error {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		navCtx, cancel := context.WithTimeout(ctx, timeout)
		err := chromedp.Run(navCtx, chromedp.Navigate(u))
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		sleepCtx, sleepCancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		_ = chromedp.Run(sleepCtx, chromedp.Sleep(1500*time.Millisecond))
		sleepCancel()
	}
	return fmt.Errorf("تعذّر التنقّل إلى %s بعد %d محاولة: %w", u, retries+1, lastErr)
}

// waitForChallengeToClear يقابل waitForChallengeToClear في النسخة
// السابقة: يفحص عنوان الصفحة+نصها كل ثانيتين بحثاً عن أي من
// challengeMarkers، حتى deadline. لا تُرجع خطأً أبداً (فشل الفحص أو
// انتهاء المهلة كلاهما يُكملان الكشط بأفضل ما هو متاح، تماماً كما في
// try/catch الأصلي الذي يُعيد true عند أي استثناء أثناء الفحص).
func waitForChallengeToClear(ctx context.Context, maxWait time.Duration, markers []string) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		var title, body string
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		errTitle := chromedp.Run(checkCtx, chromedp.Title(&title))
		if errTitle == nil {
			_ = chromedp.Run(checkCtx, chromedp.Evaluate(`document.body ? document.body.innerText : ''`, &body))
		}
		cancel()
		if errTitle != nil {
			return // الصفحة تغيّرت أثناء الفحص — على الأغلب انتقلنا فعلاً
		}
		combined := strings.ToLower(title + " " + body)
		found := false
		for _, m := range markers {
			if strings.Contains(combined, m) {
				found = true
				break
			}
		}
		if !found {
			return
		}
		sleepCtx, sleepCancel := context.WithTimeout(ctx, 3*time.Second)
		_ = chromedp.Run(sleepCtx, chromedp.Sleep(2*time.Second))
		sleepCancel()
	}
}
