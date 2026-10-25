package browser

// stealth.go — سكربت إخفاء الأتمتة وتلميحات User-Agent.

import (
	"context"
	"strings"

	"github.com/chromedp/cdproto/emulation"
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
if (ua) {
    Object.defineProperty(screen, 'colorDepth', { get: () => 24 });
    Object.defineProperty(screen, 'pixelDepth', { get: () => 24 });
    Object.defineProperty(window, 'devicePixelRatio', { get: () => 1 });
}
`

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
