package browser

// scrape.go — استخراج بيانات الصفحات بقواعد ExtractRule.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly/v2"
)

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
