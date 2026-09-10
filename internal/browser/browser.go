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
	"sync"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly/v2"
)

// stealthInitScript: سكربت stealth احترافي (نسخة 2026) يُحقن في كل صفحة
// جديدة *قبل* أي جافاسكربت آخر عبر page.AddScriptToEvaluateOnNewDocument
// — المكافئ الدقيق في CDP لِـ context.addInitScript في Playwright.
// يغطي كل فحوصات البصمة المعروفة التي تميز متصفح آلي عن بشري:
//   - navigator.webdriver + آثار CDP (domAutomation)
//   - chrome.runtime حقيقي (فحص CF يستخدمه)
//   - WebGL vendor/renderer واقعي (canvas fingerprint)
//   - بصمة جهاز كاملة: hardwareConcurrency/deviceMemory/platform/
//     languages/maxTouchPoints/connection
//   - تطبيع screen/viewport/window لتكون متناسقة
const stealthInitScript = `
// ─── 1. إخفاء آثار الأتمتة الأساسية ──────────────────────────────────
delete navigator.__proto__.webdriver;
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
if ('domAutomation' in window) { delete window.domAutomation; }
if ('domAutomationController' in window) { delete window.domAutomationController; }

// ─── 2. chrome.runtime حقيقي ────────────────────────────────────────
// Cloudflare وغيرها يفحصون وجود runtime.connect و runtime.onInstalled
// — الكائن الوهمي الفارغ {runtime: {}} علامة كشف فورية.
const fakeChrome = window.chrome || {};
if (!fakeChrome.runtime || fakeChrome.runtime.connect === undefined) {
    const connectImpl = function(extensionId, connectInfo) {
        const id = Math.random().toString(36).slice(2, 10);
        return {
            name: connectInfo && connectInfo.name ? connectInfo.name : '',
            disconnect: function(){},
            onDisconnect: { addListener: function(){}, removeListener: function(){} },
            onMessage: { addListener: function(){}, removeListener: function(){} },
            postMessage: function(){},
            sender: { id: extensionId || undefined },
        };
    };
    connectImpl.lastError = undefined;
    fakeChrome.runtime = {
        id: undefined,
        connect: connectImpl,
        sendMessage: function(message, responseCallback){ if (typeof responseCallback === 'function') responseCallback(undefined); },
        getURL: function(path){ return 'chrome-extension://' + (this.id || 'invalid') + '/' + path; },
        onInstalled: { addListener: function(){}, removeListener: function(){} },
        onMessage: { addListener: function(){}, removeListener: function(){} },
        lastError: undefined,
    };
    window.chrome = fakeChrome;
}

// ─── 3. WebGL واقعي (canvas fingerprint) ────────────────────────────
(function patchWebGL() {
    const realGetContext = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function(type, attrs) {
        const ctx = realGetContext.apply(this, arguments);
        if (!ctx) return ctx;
        if (type === 'webgl' || type === 'experimental-webgl' ||
            type === 'webgl2' || type === 'experimental-webgl2') {
            if (!ctx.__patched) {
                ctx.__patched = true;
                const fakeParams = {};
                fakeParams[0x1F00] = 'Intel Inc.';          // UNMASKED_VENDOR_WEBGL
                fakeParams[0x1F01] = 'Intel Iris OpenGL Engine'; // UNMASKED_RENDERER_WEBGL
                const realGP = ctx.getParameter.bind(ctx);
                ctx.getParameter = function(param) {
                    if (param in fakeParams) return fakeParams[param];
                    return realGP(param);
                };
            }
        }
        return ctx;
    };
})();

// ─── 4. بصمة جهاز بشرية كاملة ───────────────────────────────────────
Object.defineProperty(navigator, 'hardwareConcurrency', { get: () => 8 });
Object.defineProperty(navigator, 'deviceMemory', { get: () => 8 });
Object.defineProperty(navigator, 'platform', { get: () => '__STEALTH_PLATFORM__' });
Object.defineProperty(navigator, 'maxTouchPoints', { get: () => 0 });
Object.defineProperty(navigator, 'languages', {
    get: () => ['en-US', 'en'], configurable: true, enumerable: true
});
Object.defineProperty(navigator, 'plugins', {
    get: () => {
        const p = [
            { name: 'PDF Viewer', filename: 'internal-pdf-viewer' },
            { name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai' },
            { name: 'Chromium PDF Viewer', filename: 'internal-pdf-viewer' },
            { name: 'Microsoft Edge PDF Viewer', filename: 'internal-pdf-viewer' },
            { name: 'WebKit built-in PDF', filename: 'internal-pdf-viewer' },
        ];
        p.forEach((item, i) => {
            item.__proto__ = Plugin.prototype;
            p[i] = item;
        });
        p.__proto__ = PluginArray.prototype;
        return p;
    }, configurable: true, enumerable: true
});
Object.defineProperty(navigator, 'mimeTypes', {
    get: () => [
        { type: 'application/pdf', suffixes: 'pdf', description: 'Portable Document Format' },
    ], configurable: true, enumerable: true
});
// network connection (تستخدمه بعض فحوصات البصمة)
Object.defineProperty(navigator, 'connection', {
    get: () => ({
        effectiveType: '4g', rtt: 50, downlink: 10,
        saveData: false, onchange: null,
    }), configurable: true, enumerable: true
});
Object.defineProperty(navigator, 'permissions', {
    get: () => {
        const p = navigator.__proto__ ? navigator.__proto__.permissions : undefined;
        const orig = p ? p.query.bind(p) : undefined;
        return {
            query: function(params) {
                return (params && params.name === 'notifications')
                    ? Promise.resolve({ state: Notification.permission, onchange: null })
                    : orig ? orig(params) : Promise.resolve({ state: 'granted', onchange: null });
            }
        };
    }, configurable: true
});

// ─── 5. تطبيع الشاشة والنافذة (متناسقة مع WindowSize) ──────────────
const ua = navigator.userAgent || '';
const isMac = /Macintosh/.test(ua), isLinux = /Linux/.test(ua);
if (ua) {
    Object.defineProperty(screen, 'colorDepth', { get: () => 24 });
    Object.defineProperty(screen, 'pixelDepth', { get: () => 24 });
    Object.defineProperty(window, 'devicePixelRatio', { get: () => 1 });
}
`

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

// extractBrandVersion يستخرج الرقم الرئيسي من UA مثل "Chrome/151.0.0.0".
func extractBrandVersion(ua string) string {
	if i := strings.Index(ua, "Chrome/"); i >= 0 {
		s := ua[i+7:]
		if j := strings.IndexByte(s, '.'); j > 0 {
			return s[:j]
		}
	}
	return "151"
}

// setUAHints يضبط User-Agent Client Hints (Sec-CH-UA) عبر CDP — CF
// يفحص Sec-CH-UA منفصلاً عن UA، فبدون مواءمتهما معاً يكون UA الحديث
// وحده غير كافٍ وقد يزيد التناقض في البصمة.
func setUAHints(ctx context.Context, ua string) {
	ver := extractBrandVersion(ua)
	// brands القياسية لما يرسله Chrome الحقيقي: brand الرئيسي + علامة
	// "Not A(Brand";v=99" + "Chromium" (تُفعل من Chrome 110+).
	brands := []*emulation.UserAgentBrandVersion{
		{Brand: "Not)A;Brand", Version: "8"},
		{Brand: "Chromium", Version: ver},
		{Brand: "Google Chrome", Version: ver},
	}
	platform := "Windows"
	platformVersion := "19.0.0"
	if strings.Contains(ua, "Macintosh") {
		platform = "macOS"
		platformVersion = "15.0.0"
	} else if strings.Contains(ua, "X11") {
		platform = "Linux"
		platformVersion = "6.1.0"
	}
	_ = emulation.SetUserAgentOverride(ua).
		WithPlatform(platform).
		WithAcceptLanguage("en-US,en;q=0.9").
		WithUserAgentMetadata(&emulation.UserAgentMetadata{
			Brands:          brands,
			FullVersionList: brands,
			Platform:        platform,
			PlatformVersion: platformVersion,
			Architecture:    "x86",
			Model:           "",
			Mobile:          false,
		}).Do(ctx)
}

// platformForUA يعيد قيمة navigator.platform الحقيقية المطابقة لنظام UA
// المُختار (Win32 لويندوز، MacIntel لماك، Linux x86_64 لينكس) — هذه
// القيم *مختلفة نصياً* عن Sec-CH-UA-Platform التي يضبطها setUAHints
// ("Windows"/"macOS"/"Linux")، وهذا طبيعي ومطابق لسلوك Chrome الحقيقي.
// المشكلة التي يحلّها هذا: stealthInitScript كان يفرض navigator.platform
// = 'Win32' حرفياً بلا شرط، بينما RandomUserAgent() تختار أحياناً UA
// ماك (macOS) من humanUA — تناقض بصمة فاضح (UA يقول Mac وplatform يقول
// Win32) يكشفه أي فحص Cloudflare بسيط ويرفض الجلسة فوراً. من هنا جاء
// "الحجب حتى مع محاولات ناجحة سابقاً" الذي أبلغ عنه المستخدم بعد هذا
// التحديث — وليس عطلاً في mangabridge أو novel أنفسهما، بل في كل جلسة
// متصفح تمر عبر browser.New (يشمل mangabridge وnovel وpinterest).
func platformForUA(ua string) string {
	switch {
	case strings.Contains(ua, "Macintosh"):
		return "MacIntel"
	case strings.Contains(ua, "X11") || strings.Contains(ua, "Linux"):
		return "Linux x86_64"
	default:
		return "Win32"
	}
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

// ============================================================
// ⭐ BEHAVIOR — سلوك بشري (2026-08)
// ============================================================

// HumanLikeWait يعيد مدة انتظار عشوائية طبيعية (أقل من ثانية عادة)
// تحاكي تردد الإنسان بين إجراء وآخر — تُستخدم بين التنقلات والتمرير.
func HumanLikeWait() time.Duration {
	return time.Duration(400+rand.Intn(1800)) * time.Millisecond
}

// MoveMouseHuman يحرك مؤشر الفأرة من النقطة الحالية إلى (x, y) عبر
// سلسلة خطوات bezier قصيرة بفواصل عشوائية — بدلاً من teleport فوري
// إلى الإحداثيات الذي تميزه فحوصات التفاعل (pointer events ذات duration=0).
func MoveMouseHuman(ctx context.Context, x, y float64) error {
	steps := 4 + rand.Intn(4) // 4–7 خطوات
	fx, fy := 0.0, 0.0
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		// إزاحة عشوائية صغيرة حول المسار المستقيم (ارتعاش يد طبيعي)
		jx := (rand.Float64() - 0.5) * 30 * (1 - t)
		jy := (rand.Float64() - 0.5) * 30 * (1 - t)
		cx := x*t + jx
		cy := y*t + jy
		if err := chromedp.Run(ctx, input.DispatchMouseEvent(
			input.MouseMoved, cx, cy).
			WithPointerType("mouse").
			WithButton(input.None),
		); err != nil {
			return err
		}
		fx, fy = cx, cy
		_ = fx
		_ = fy
		time.Sleep(time.Duration(12+rand.Intn(25)) * time.Millisecond)
	}
	return nil
}

// AwaitCloudflare ينتظر زوال عناصر تحدي Cloudflare بذكاء: يتحقق دورياً
// بفاصل عشوائي قصير (200–600ms) حتى تختفي جميع عناصر التحدي المعروفة أو
// انتهاء مهلة ctx. لا يوجد Sleep ثابت — التحقق يتم وفق توقيت طبيعي،
// وبنهاية الانتظار نفحص عنوان الصفحة (challenge يترك عنوان "Just a
// moment..." حتى بعد إخفاء العناصر أحياناً).
//
// يعيد nil عند النجاح، أو خطأ timeout إن بقي التحدي قائماً.
func AwaitCloudflare(ctx context.Context) error {
	queries := [][2]interface{}{
		{"#challenge-form", chromedp.ByID},
		{"#cf-challenge-running", chromedp.ByID},
		{".cf-browser-verification", chromedp.ByQuery},
	}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		allGone := true
		for _, q := range queries {
			sel, _ := q[0].(string)
			opt, _ := q[1].(chromedp.QueryOption)
			if err := chromedp.Run(ctx, chromedp.WaitNotPresent(sel, opt)); err != nil {
				allGone = false
				break
			}
		}
		if allGone {
			// تأكد إضافي: عنوان "Just a moment..." يعني أن التحدي ما زال
			// نشطاً رغم اختفاء العناصر (مرحلة تحقق خفية).
			var title string
			if err := chromedp.Run(ctx, chromedp.Title(&title)); err == nil &&
				strings.Contains(strings.ToLower(title), "just a moment") {
				allGone = false
			}
		}
		if allGone {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(200+rand.Intn(400)) * time.Millisecond):
		}
	}
	return fmt.Errorf("لم يُحلّ تحدي Cloudflare خلال المهلة")
}

// ============================================================
// ⭐ SCRAPE — استخراج DOM من صفحة (جديد 2026-07)
// ============================================================
//
// Scrape() تستخدم نفس جلسة chromedp (عبر New() أعلاه) للتنقل إلى URL،
// انتظار زوال تحدي Cloudflare عبر AwaitCloudflare() الذكي، ثم استخراج
// البيانات حسب extractRules عبر Colly.
//
// هذا يعني أن pinterest وmanga_bridge وأي plugin مستقبلي *كلهم* يستخدمون
// نفس بنية التشغيل الأساسية (stealth script، no-sandbox، user agent
// عشوائي، سلوك بشري) — لا تكرار، لا inconsistencies.
//
// الاستخدام: في أي plugin، استدعِ browser.Scrape(ctx, url, rules, opts)
// مباشرة — لا حاجة لplugin scraper منفصل.

// ExtractRule تحدد قاعدة استخراج واحدة: selector CSS + ما إذا كان
// الحقل وحيداً أم قائمة + استخراج attribute اختياري + عناصر فرعية.
type ExtractRule struct {
	Selector string            `json:"selector"`
	Attr     string            `json:"attr,omitempty"`
	Multiple bool              `json:"multiple"`
	Children map[string]string `json:"children,omitempty"`
}

// ExtractedItem يحمل قيمة مستخرجة مع عناصرها الفرعية (لـ Multiple=true + Children).
type ExtractedItem struct {
	Value    string            `json:"value"`
	Children map[string]string `json:"children,omitempty"`
}

// ScrapeResult يحمل نتائج الاستخراج كاملة (آمن للاستخدام المتزامن).
type ScrapeResult struct {
	mu   sync.RWMutex
	Data map[string]any
}

// NewScrapeResult يُنشئ result فارغاً.
func NewScrapeResult() *ScrapeResult {
	return &ScrapeResult{Data: make(map[string]any)}
}

// All يعيد نسخة من كل البيانات (بـ RLock).
func (r *ScrapeResult) All() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]any, len(r.Data))
	for k, v := range r.Data {
		out[k] = v
	}
	return out
}

// Scrape ينتقل إلى urlStr عبر جلسة chromedp (تُنشأ من New() داخلياً)،
// ينتظر زوال تحدي Cloudflare عبر AwaitCloudflare() الذكي، ثم يستخرج
// البيانات حسب extractRules عبر Colly.
//
// opts: خيارات جلسة المتصفح (نفس Options في New() — يمكن تمرير
// ExecPathEnv="SCRAPE_CHROMIUM_PATH" مثلاً).
//
// يُعيد *ScrapeResult جاهزاً للقراءة، أو خطأ لو فشل التنقل/الاستخراج.
// المستدعي مسؤول عن التحقق من أن النتائج غير فارغة إن كان ذلك ضرورياً.
func Scrape(ctx context.Context, urlStr string, extractRules map[string]ExtractRule, opts Options) (*ScrapeResult, error) {
	if urlStr == "" {
		return nil, fmt.Errorf("empty URL")
	}

	bctx, cancel, err := New(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("browser.New failed: %w", err)
	}
	defer cancel()

	var htmlContent string
	if err := chromedp.Run(bctx, chromedp.Navigate(urlStr)); err != nil {
		return nil, fmt.Errorf("chromedp navigation failed: %w", err)
	}
	if err := AwaitCloudflare(bctx); err != nil {
		return nil, fmt.Errorf("cloudflare challenge not passed: %w", err)
	}
	// انتظار طبيعي قصير بعد حل التحدي (سلوك بشري) بدل Sleep ثابت:
	if err := chromedp.Run(bctx, chromedp.Sleep(HumanLikeWait())); err != nil {
		return nil, fmt.Errorf("post-challenge wait failed: %w", err)
	}
	if err := chromedp.Run(bctx, chromedp.OuterHTML("html", &htmlContent)); err != nil {
		return nil, fmt.Errorf("html extraction failed: %w", err)
	}

	result := NewScrapeResult()
	var collyWg sync.WaitGroup
	c := colly.NewCollector()

	for key, rule := range extractRules {
		k := key
		r := rule
		c.OnHTML(r.Selector, func(e *colly.HTMLElement) {
			collyWg.Add(1)
			defer collyWg.Done()

			value := ""
			if r.Attr != "" {
				value = e.Attr(r.Attr)
			} else {
				value = strings.TrimSpace(e.Text)
			}

			result.mu.Lock()
			defer result.mu.Unlock()

			if r.Multiple {
				if len(r.Children) > 0 {
					childData := make(map[string]string)
					for ck, csel := range r.Children {
						childData[ck] = strings.TrimSpace(e.ChildText(csel))
					}
					item := ExtractedItem{Value: value, Children: childData}
					existing, ok := result.Data[k]
					if !ok {
						result.Data[k] = []ExtractedItem{item}
					} else if slice, ok := existing.([]ExtractedItem); ok {
						result.Data[k] = append(slice, item)
					} else {
						result.Data[k] = []ExtractedItem{item}
					}
				} else {
					existing, ok := result.Data[k]
					if !ok {
						result.Data[k] = []string{value}
					} else if slice, ok := existing.([]string); ok {
						result.Data[k] = append(slice, value)
					} else {
						result.Data[k] = []string{value}
					}
				}
			} else {
				result.Data[k] = value
			}
		})
	}

	// colly.Collector ليس لديها طريقة لتحليل نص HTML جاهز في الذاكرة مباشرة
	// (لا توجد ParseBytes) — هي مبنية حول Visit(URL) عبر HTTP فقط. لذا
	// نُنشئ خادم HTTP محلي مؤقت يخدم htmlContent (القادم من chromedp)،
	// ثم نجعل colly يزوره كأي صفحة عادية؛ هذا يبقينا ضمن colly بالكامل
	// لعملية الاستخراج نفسها (OnHTML/ChildText/...).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlContent))
	}))
	defer ts.Close()

	if err := c.Visit(ts.URL); err != nil {
		return nil, fmt.Errorf("colly visit error: %w", err)
	}
	collyWg.Wait()

	return result, nil
}
