package mangabridge

// scraper.go — كشط صفحة الفصل عبر المتصفح.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"sunkenbot/internal/browser"
)

// ─── محرّك الكشط (chromedp/CDP خالص Go — بدل Node.js/Playwright سابقاً) ─
//
// ملاحظة صادقة: يحتاج Chromium مثبَّتاً فعلياً على الجهاز الذي يعمل عليه
// هذا الثنائي (راجع الـ Dockerfile). لم يتسنَّ اختبار هذا بمتصفح حقيقي
// في بيئة تطوير هذا المشروع (تحميل ثنائي Chromium نفسه محجوب شبكياً
// هناك) — لكن حزمة chromedp نفسها بُنيت واختُبرت فعلياً هنا (راجع
// internal/browser). حتى نسخة بايثون الأصلية توثّق أن Cloudflare قد
// يحجب هذا بغض النظر عن الأداة، فهذا الجزء هش حتى في أفضل الأحوال.

const (
	mangaBaseURL          = "https://3asq.online"
	mangaCFChallengeWaitS = 15
	mangaUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

// waitForCloudflareTitle يقابل waitForCloudflare في manga_scraper.js
// السابق: يفحص عنوان الصفحة كل ثانية حتى deadline، بحثاً عن زوال
// "Just a moment" من العنوان.
func waitForCloudflareTitle(ctx context.Context, maxWaitS int) bool {
	for range maxWaitS {
		var title string
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := chromedp.Run(checkCtx, chromedp.Title(&title))
		cancel()
		if err == nil && !strings.Contains(title, "Just a moment") {
			return true
		}
		sleepCtx, sleepCancel := context.WithTimeout(ctx, 2*time.Second)
		_ = chromedp.Run(sleepCtx, chromedp.Sleep(1*time.Second))
		sleepCancel()
	}
	var title string
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := chromedp.Run(checkCtx, chromedp.Title(&title))
	cancel()
	return err == nil && !strings.Contains(title, "Just a moment")
}

const extractImgSrcsJS = `
(() => {
    const imgs = document.querySelectorAll('.page-break img, .reading-content img');
    const out = [];
    const seen = new Set();
    for (const img of imgs) {
        const src = img.getAttribute('data-src') || img.getAttribute('data-lazy-src') || img.getAttribute('src');
        if (!src || !src.trim()) continue;
        try {
            const absolute = new URL(src.trim(), document.baseURI);
            if (absolute.protocol !== 'https:' || seen.has(absolute.href)) continue;
            seen.add(absolute.href);
            out.push(absolute.href);
            if (out.length >= 300) break;
        } catch (_) {}
    }
    return out;
})()
`

const firstSearchResultHrefJS = `
(() => {
    const a = document.querySelector('.page-item-detail .item-thumb a');
    return a ? (a.getAttribute('href') || '') : '';
})()
`

type scrapedChapter struct {
	title string
	url   string
	srcs  []string
}

// scrapeChapterDOM يقابل الجزء الأول (بلا تحميل الصور) من scrapeChapter
// في manga_scraper.js السابق: تنقّل، انتظار Cloudflare، استخراج روابط
// الصور، ومع منطق fallback البحث لو لم توجد صور مباشرة.
func scrapeChapterDOM(ctx context.Context, manga, chapter string) (scrapedChapter, error) {
	slug := slugify(manga)
	chapterURL := fmt.Sprintf("%s/manga/%s/%s/", mangaBaseURL, slug, chapter)

	navCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	err := chromedp.Run(navCtx, chromedp.Navigate(chapterURL))
	cancel()
	if err != nil {
		return scrapedChapter{}, fmt.Errorf("تعذّر فتح صفحة الفصل: %w", err)
	}
	waitForCloudflareTitle(ctx, mangaCFChallengeWaitS)

	var srcs []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(extractImgSrcsJS, &srcs)); err != nil {
		return scrapedChapter{}, fmt.Errorf("فشل استخراج روابط الصور: %w", err)
	}

	if len(srcs) == 0 {
		// لا صور — جرّب البحث (نفس منطق manga2.js الأصلي)
		searchURL := fmt.Sprintf("%s/?s=%s", mangaBaseURL, url.QueryEscape(manga))
		searchCtx, searchCancel := context.WithTimeout(ctx, 30*time.Second)
		err := chromedp.Run(searchCtx, chromedp.Navigate(searchURL))
		searchCancel()
		if err != nil {
			return scrapedChapter{}, fmt.Errorf("تعذّر فتح صفحة البحث: %w", err)
		}
		waitForCloudflareTitle(ctx, mangaCFChallengeWaitS)

		var href string
		if err := chromedp.Run(ctx, chromedp.Evaluate(firstSearchResultHrefJS, &href)); err != nil || href == "" {
			return scrapedChapter{title: "", url: "", srcs: nil}, nil
		}

		retryURL := strings.TrimRight(href, "/") + "/" + chapter + "/"
		retryCtx, retryCancel := context.WithTimeout(ctx, 45*time.Second)
		err = chromedp.Run(retryCtx, chromedp.Navigate(retryURL))
		retryCancel()
		if err != nil {
			return scrapedChapter{}, fmt.Errorf("تعذّر فتح رابط الفصل البديل: %w", err)
		}
		waitForCloudflareTitle(ctx, mangaCFChallengeWaitS)

		if err := chromedp.Run(ctx, chromedp.Evaluate(extractImgSrcsJS, &srcs)); err != nil {
			return scrapedChapter{}, fmt.Errorf("فشل استخراج روابط الصور (بعد البحث): %w", err)
		}
	}

	var title, finalURL string
	_ = chromedp.Run(ctx, chromedp.Title(&title), chromedp.Location(&finalURL))

	if len(srcs) == 0 {
		return scrapedChapter{title: title, url: finalURL, srcs: nil}, nil
	}
	return scrapedChapter{title: title, url: finalURL, srcs: srcs}, nil
}

// browserCookieHeader يستخرج كل الكوكيز الحالية في جلسة chromedp (تشمل
// أي cf_clearance حصلت عليه الجلسة من اجتياز تحدي Cloudflare) ويبنيها
// كسلسلة رأس Cookie قياسية — يُستخدم لاحقاً في fetchImages ليطلب Go
// نفس الصور بنفس هوية الجلسة التي اجتازت التحدي، بدل أن يبدأ من صفر
// ويصطدم بالتحدي من جديد (نفس أثر مشاركة browser context بين page.goto
// وcontext.request.get في Playwright/Node السابق).
func browserCookieHeader(ctx context.Context, forURL string) (string, error) {
	if err := chromedp.Run(ctx, network.Enable()); err != nil {
		return "", err
	}
	var cookies []*network.Cookie
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithURLs([]string{forURL}).Do(ctx)
		return err
	})); err != nil {
		return "", err
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; "), nil
}

// scrapeChapter يقابل الدالة بنفس الاسم في manga_bridge.py الأصلي
// (وscrapeChapter في manga_scraper.js السابق قبل حذفه): تشغّل جلسة
// Chromium مخصصة كاملة، تكشط DOM صفحة الفصل، ثم تُحمِّل الصور
// وتكتبها بترتيب الصفحات وامتداد يطابق نوع الصورة داخل outputDir.
func (s *Service) scrapeChapter(ctx context.Context, manga, chapter, outputDir string) (title, chapterURL string, imageCount int, err error) {
	// حارس التزامن: يحد عدد نسخ Chromium المتشغّلة معاً.
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ctxTimeout, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	bctx, bcancel, err := browser.New(ctxTimeout, browser.Options{
		UserAgent:   mangaUserAgent,
		ExecPathEnv: "MANGA_CHROMIUM_PATH",
	})
	if err != nil {
		return "", "", 0, err
	}
	defer bcancel()

	result, err := scrapeChapterDOM(bctx, manga, chapter)
	if err != nil {
		return "", "", 0, err
	}
	if len(result.srcs) == 0 {
		return result.title, result.url, 0, nil
	}

	cookieHeader, _ := browserCookieHeader(bctx, result.url)
	buffers := fetchImages(ctxTimeout, result.srcs, result.url, cookieHeader)
	if len(buffers) == 0 {
		return result.title, result.url, 0, nil
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", 0, fmt.Errorf("تعذّر إنشاء مجلد الصور: %w", err)
	}
	for idx, buf := range buffers {
		fpath := filepath.Join(outputDir, fmt.Sprintf("%d%s", idx, imageFileExtension(buf)))
		if err := os.WriteFile(fpath, buf, 0o644); err != nil {
			return "", "", 0, fmt.Errorf("تعذّر كتابة الصورة %d: %w", idx, err)
		}
	}

	return result.title, result.url, len(buffers), nil
}
