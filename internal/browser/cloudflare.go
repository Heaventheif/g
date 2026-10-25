package browser

// cloudflare.go — انتظار اجتياز تحدّي Cloudflare.

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	defaultCFTimeout      = 25 * time.Second
	defaultCFCheckTimeout = 3 * time.Second
)

// CloudflareOptions يضبط Await. القيم الصفرية = الافتراضيات (سلوك AwaitCloudflare القديم).
type CloudflareOptions struct {
	// Timeout: السقف الكلي للانتظار (الافتراضي 25s) — يُطبَّق فعلياً عبر context،
	// فلا يستطيع فحص عالق تجاوزه (WaitNotPresent السابق بلا مهلة كان يتجاوزه).
	Timeout time.Duration
	// CheckTimeout: مهلة كل فحص منفرد (الافتراضي 3s).
	CheckTimeout time.Duration
	// TitleMarkers: نصوص (حروف صغيرة) إن وُجد أيٌّ منها في عنوان الصفحة فالتحدي
	// قائم. الافتراضي {"just a moment"}.
	TitleMarkers []string
	// BodyMarkers: نصوص (حروف صغيرة) تُفحص في document.body.innerText؛ فارغة = لا فحص للنص.
	BodyMarkers []string
}

func (o CloudflareOptions) withDefaults() CloudflareOptions {
	if o.Timeout <= 0 {
		o.Timeout = defaultCFTimeout
	}
	if o.CheckTimeout <= 0 {
		o.CheckTimeout = defaultCFCheckTimeout
	}
	if len(o.TitleMarkers) == 0 {
		o.TitleMarkers = []string{"just a moment"}
	}
	return o
}

// probeResult نتيجة فحص واحد للصفحة.
type probeResult struct {
	ElementPresent bool   `json:"present"`
	Title          string `json:"title"`
	Body           string `json:"body"`
}

// challengeActive: هل ما زال التحدي قائماً وفق النتيجة والخيارات؟ (دالة نقية للاختبار)
func (p probeResult) challengeActive(o CloudflareOptions) bool {
	if p.ElementPresent {
		return true
	}
	title := strings.ToLower(p.Title)
	for _, m := range o.TitleMarkers {
		if m != "" && strings.Contains(title, m) {
			return true
		}
	}
	if len(o.BodyMarkers) > 0 {
		body := strings.ToLower(p.Body)
		for _, m := range o.BodyMarkers {
			if m != "" && strings.Contains(body, m) {
				return true
			}
		}
	}
	return false
}

// فحص واحد بلا انتظار: عناصر التحدي المعروفة + العنوان (+ نص الصفحة عند الطلب).
const (
	cfProbeJS = `(() => ({
		present: !!document.querySelector('#challenge-form, #cf-challenge-running, .cf-browser-verification'),
		title: document.title || '',
		body: ''
	}))()`
	cfProbeBodyJS = `(() => ({
		present: !!document.querySelector('#challenge-form, #cf-challenge-running, .cf-browser-verification'),
		title: document.title || '',
		body: document.body ? document.body.innerText : ''
	}))()`
)

func chromeProbe(withBody bool) func(ctx context.Context) (probeResult, error) {
	js := cfProbeJS
	if withBody {
		js = cfProbeBodyJS
	}
	return func(ctx context.Context) (probeResult, error) {
		var r probeResult
		err := chromedp.Run(ctx, chromedp.Evaluate(js, &r))
		return r, err
	}
}

// AwaitCloudflare ينتظر زوال تحدي Cloudflare بالافتراضيات (25s). غلاف على Await.
func AwaitCloudflare(ctx context.Context) error {
	return Await(ctx, CloudflareOptions{})
}

// Await ينتظر زوال تحدي Cloudflare: يفحص دورياً بفاصل عشوائي قصير (200–600ms)،
// لكل فحص مهلته الخاصة (CheckTimeout) وللكل سقف Timeout حقيقي.
// يعيد nil عند الزوال، ctx.Err() عند إلغاء المستدعي، أو خطأ مهلة إن بقي التحدي.
func Await(ctx context.Context, opts CloudflareOptions) error {
	opts = opts.withDefaults()
	return awaitWith(ctx, opts, chromeProbe(len(opts.BodyMarkers) > 0))
}

func awaitWith(parent context.Context, opts CloudflareOptions, probe func(context.Context) (probeResult, error)) error {
	ctx, cancel := context.WithTimeout(parent, opts.Timeout)
	defer cancel()
	for {
		checkCtx, checkCancel := context.WithTimeout(ctx, opts.CheckTimeout)
		res, err := probe(checkCtx)
		checkCancel()
		// خطأ الفحص (تنقّل جارٍ، سياق مُدمَّر...) = ما زال غير جاهز، نعيد المحاولة.
		if err == nil && !res.challengeActive(opts) {
			return nil
		}
		select {
		case <-ctx.Done():
			if perr := parent.Err(); perr != nil {
				return perr
			}
			return fmt.Errorf("لم يُحلّ تحدي Cloudflare خلال المهلة")
		case <-time.After(time.Duration(200+rand.Intn(400)) * time.Millisecond):
		}
	}
}
