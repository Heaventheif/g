// Package browser يوفّر بنية تحتية مشتركة لتشغيل Chrome بلا واجهة
// (headless) عبر chromedp — مكتبة Go خالصة تتحدَّث مباشرة مع Chrome
// عبر بروتوكول Chrome DevTools (CDP)، بلا أي وسيط. هذا يستبدل استدعاء
// Node.js/Playwright كعملية فرعية عبر stdin/stdout (الذي كان يُستخدَم
// سابقاً في plugins/pinterest عبر scripts/pinterest/pinterest_scraper.js
// وplugins/mangabridge عبر scripts/mangabridge/manga_scraper.js).
//
// كلا الـ plugin يحتاجان نفس منطق التشغيل بالضبط (headless، no-sandbox،
// حقن سكربت stealth، user agent عشوائي)، لذا استُخرج هنا بدل تكراره —
// نفس فلسفة internal/netguard وinternal/session: بنية تحتية مشتركة بين
// عدة plugins، وليست هي نفسها plugins.Service.
//
// ⭐ إضافة (2026-07): Scrape() — استخراج DOM من صفحة بعد التنقل عبر
// chromedp، باستخدام Colly لتحليل HTML. أي plugin يحتاج استخراج بيانات
// من صفحة ويب (مثل pinterest، manga_bridge، أو أي plugin مستقبلي) يمكنه
// استخدامها مباشرة بدلاً من تكرار منطق chromedp + HTML parsing.
//
// ⭐ تقوية 2026-08 (بطلب المستخدم — "متصفح موحد أقوى"):
//   - stealthInitScript 2026: إخفاء أعمق لآثار الأتمتة — chrome.runtime
//     حقيقي (connect/onInstalled/lastError)، WebGL vendor/renderer طبيعي،
//     بصمة جهاز كاملة (hardwareConcurrency/deviceMemory/platform/
//     maxTouchPoints/languages)، إزالة آثار CDP (domAutomation/
//     AutomationControlled في cmdline)، تطبيع screen/viewport.
//   - HumanUA(): قائمة UAs محدثة 2026 (Chrome 131-151) مع Sec-CH-UA
//     مضبوط عبر CDP Page.setUserAgentOverride لكل جلسة (brands/platform/
//     version/moblie) — لأن CF يقرّأ UA-CH منفصلاً عن UA.
//   - تعطيل enable-automation (chromedp يضيفه افتراضياً — علامة كشف).
//   - Humanize: تحريك ماوس bezier واقعي إلى نقطة قبل التفاعل، انتظار
//     عشوائي بين الطلبات — سلوك أقرب للبشر.
//   - AwaitCloudflare(): انتظار ذكي لتجاوز تحدي CF بدل Sleep(2) الثابت:
//     يتحقق دورياً بفاصل عشوائي قصير حتى زوال عناصر التحدي.
package browser

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// humanUA: قائمة user agents محدثة 2026 (Chrome 131–151 المستقرة).
// Cloudflare يرفض/يشك في UAs قديمة (Chrome/125 مثلاً) لأنها لا تصدر منذ
// أكثر من عام — بصمة زمنية متناقضة مع سلوك "متصفح بشري".
var humanUA = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
}

// userAgentsLegacy تبقى متاحة للتوافق مع الكود القديم الذي يعتمد على
// RandomUserAgent() — نفس القائمة السابقة حرفياً.
var userAgentsLegacy = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
}

// RandomUserAgent يعيد user agent عشوائياً من القائمة الحديثة (2026).
// الكود القديم الذي كان يعتمد على القائمة السابقة ينتقل تلقائياً للقائمة
// الأحدث — هذا مقصود لأن القائمة القديمة أصبحت بصمة كشف.
func RandomUserAgent() string {
	return humanUA[rand.Intn(len(humanUA))]
}

// RandomUserAgentLegacy: القائمة القديمة للتوافق الكامل.
func RandomUserAgentLegacy() string {
	return userAgentsLegacy[rand.Intn(len(userAgentsLegacy))]
}

// Options يخصص جلسة متصفح واحدة.
type Options struct {
	// UserAgent: فارغ = يُختار عشوائياً عبر RandomUserAgent() (الحديثة).
	UserAgent string
	// WindowWidth/WindowHeight: فارغ (0) = لا يُضبط حجم نافذة صراحة
	// (يُستخدم افتراضي Chromium).
	WindowWidth  int
	WindowHeight int
	// ExecPathEnv: اسم متغير البيئة الذي قد يحدد مساراً مخصصاً لثنائي
	// Chromium (مطابق لـ PINTEREST_CHROMIUM_PATH / MANGA_CHROMIUM_PATH
	// السابقين) — كل plugin يمرر اسم متغيره الخاص هنا.
	ExecPathEnv string
}

// New يخصص Chrome headless جديد كلياً (سياق exec allocator منفصل تماماً
// عن أي جلسة أخرى — لا يشارك ملف تعريف مستخدم ولا ذاكرة تخزين مؤقت مع
// أي طلب متزامن آخر، تماماً كما كان كل استدعاء subprocess Node مستقلاً
// بالكامل سابقاً)، يفتح تبويباً جديداً، ويحقن سكربت stealth فورياً.
//
// يُعيد context.Context جاهزاً للتمرير مباشرة لـ chromedp.Run، ودالة
// تنظيف واحدة تُغلق كل شيء (التبويب + عملية Chromium الفرعية بالكامل)
// — استدعها عبر defer فور نجاح New. مهلة/إلغاء parent (عادة عبر
// context.WithTimeout في الـ plugin المستدعي) تُطبَّق على كامل الجلسة،
// بما فيها تشغيل Chromium نفسه — نفس أثر context.WithTimeout الذي كان
// يُلف حول exec.CommandContext سابقاً.
//
// تحسّنات التجاوز (2026-08):
//   - تعطيل enable-automation: chromedp يضيف هذا الفلاج افتراضياً وهو
//     أبرز علامة في cmdline يقرأها Cloudflare — نوقفه صراحة.
//   - --lang=en-US: تطبيع لغة واجهة Chromium نفسها (بدون flag تكون
//     لغة نظام التشغيل داخل الـ container — Linux = "en" فقط = بصمة).
//   - user-agent-data-override: Sec-CH-UA متسق مع UA.
func New(parent context.Context, opts Options) (context.Context, context.CancelFunc, error) {
	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		// تعطيل enable-automation — أهم علامة كشف يضيفها chromedp
		// افتراضياً إلى cmdline (يقرأه Cloudflare وغيرها):
		chromedp.Flag("enable-automation", false),
		// لغة واجهة Chromium نفسها — بدونها تكون لغة النظام (Linux en)
		// بينما UA يقول Windows = تناقض في البصمة:
		chromedp.Flag("lang", "en-US"),
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
	)
	if opts.WindowWidth > 0 && opts.WindowHeight > 0 {
		allocOpts = append(allocOpts, chromedp.WindowSize(opts.WindowWidth, opts.WindowHeight))
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = RandomUserAgent()
	}
	allocOpts = append(allocOpts, chromedp.UserAgent(ua))

	if opts.ExecPathEnv != "" {
		if p := os.Getenv(opts.ExecPathEnv); p != "" {
			allocOpts = append(allocOpts, chromedp.ExecPath(p))
		}
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, allocOpts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	cancel := func() {
		ctxCancel()
		allocCancel()
	}

	// chromedp.Run بلا actions إضافية غير حقن stealth يخصص العملية
	// الفرعية والتبويب فوراً (بدل الانتظار حتى أول Navigate) — هذا يعطي
	// خطأ فوري واضح لو تعذّر تشغيل Chromium (ثنائي مفقود مثلاً)، بدل
	// فشل غامض لاحقاً عند أول استخدام لـ ctx.
	//
	// بعد فتح التبويب: نطبّق setUserAgentOverride الكامل (UA + platform
	// + brands + accept-language) على مستوى CDP — لأنه يتجاوز ما يضبطه
	// الفلاج ويصل لكل إطار وكل نافذة حتى بعد التنقلات.
	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// نستبدل __STEALTH_PLATFORM__ بقيمة navigator.platform الحقيقية
			// المطابقة لـ ua (راجع تعليق platformForUA) قبل الحقن — بدل
			// القيمة الثابتة 'Win32' التي كانت تناقض UAs ماك أحياناً.
			renderedScript := strings.Replace(stealthInitScript, "__STEALTH_PLATFORM__", platformForUA(ua), 1)
			if _, err := page.AddScriptToEvaluateOnNewDocument(renderedScript).Do(ctx); err != nil {
				return err
			}
			setUAHints(ctx, ua)
			return nil
		}),
	); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("تعذّر تشغيل Chromium: %w", err)
	}

	return ctx, cancel, nil
}
