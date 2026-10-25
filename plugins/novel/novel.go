// Package novel يقابل plugins/novel.py: جلب فصول الروايات من freewebnovel.com.
//
// ملاحظة تصميم: بايثون استخدمت BeautifulSoup (محلل HTML كامل + محدّدات CSS).
// المكتبات المكافئة الناضجة في Go (goquery، وحتى net/html نفسها) تعيش تحت
// golang.org/x/net، وهو نطاق كان محجوباً سابقاً في بيئة التطوير. بدل تأجيل
// الميزة، كتبنا محلّل HTML خفيف بدون أي تبعية خارجية (stdlib فقط: regexp +
// strings + html.UnescapeString) يغطي بالضبط المحددات الثابتة التي يحتاجها
// هذا الموقع تحديداً (نفس قائمة selectors في SITE ببايثون) — كافٍ ومضبوط
// لهذه الحالة الخاصة، ويبني فوراً بدون إنترنت في أي بيئة. هذا المحلّل بقي
// كما هو؛ ما تغيّر هو طريقة *جلب* الصفحة عند فشل HTTP البسيط (كلاودفلير
// مثلاً) — الآن عبر internal/browser (chromedp حقيقي) بدل تخطٍّ صامت.
//
// ملاحظة ترحيل: extract/extractTitle/slugify/isFiltered وبقية الدوال
// المساعدة النقية (بلا حالة) تبقى دوال حزمة حرة عمداً — novel_test.go
// يستدعيها مباشرة كدوال حزمة، وتحويلها لـ methods كان سيكسر تلك
// الاختبارات بلا أي فائدة بنيوية (هذه الدوال لا تحتاج أي حالة Service
// أصلاً). فقط ما يحتاج فعلياً حالة مشتركة (httpClient، الكاش) انتقل إلى
// Service.
package novel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/plugins"
	"sunkenbot/internal/strutil"
)

const Description = "جلب فصول الروايات من Freewebnovel و NovelFull مع بديل متصفح مشترك"

// Service يحمل عميل http.Client (بلا مهلة عامة — كل طلب يضبط مهلته عبر
// context، تماماً كما كان httpClient الحزمي القديم) وحالة الكاش
// (كانت جميعها متغيرات حزمة عامة: sync.Mutex + map — ممنوعة صراحة على
// مستوى الحزمة، راجع §2.2 والقيود الصارمة) كحقول صريحة.
type Service struct {
	client *http.Client

	cacheMu    sync.Mutex
	cache      map[string]cacheItem
	cacheOrder []string // لتتبّع ترتيب الإدخال (لمحاكاة next(iter(dict)) في بايثون)
}

func New(client *http.Client) *Service {
	return &Service{client: client, cache: map[string]cacheItem{}}
}

func (s *Service) Name() string { return "novel" }

func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "POST", Pattern: "/novel", Handler: httpx.Handle(s.handleGetChapter)},
		{Method: "GET", Pattern: "/novel/sites", Handler: httpx.Handle(s.handleListSites)},
		{Method: "DELETE", Pattern: "/novel/cache", Handler: httpx.Handle(s.handleClearCache)},
	}
}

// ─── الموقع (SITE في بايثون) ─────────────────────────────────────

const (
	siteName          = "Freewebnovel"
	novelFullSiteName = "NovelFull"
)

var chapterNumberPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

func parseChapterNumber(value any) (string, bool) {
	var raw string
	switch v := value.(type) {
	case float64:
		raw = strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		raw = strings.TrimSpace(v)
	default:
		return "", false
	}
	if !chapterNumberPattern.MatchString(raw) {
		return "", false
	}
	number, err := strconv.ParseFloat(raw, 64)
	if err != nil || number <= 0 || number > 100000 {
		return "", false
	}
	return raw, true
}

func normalizeNovelSite(site string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(site)) {
	case "", "freewebnovel":
		return siteName, true
	case "novelfull":
		return novelFullSiteName, true
	default:
		return "", false
	}
}

func buildChapterURL(slug, chapter string) string {
	return fmt.Sprintf("https://freewebnovel.com/novel/%s/chapter-%s", slug, chapter)
}

var contentSelectors = []contentSelector{
	{tag: "div", matchAttr: attrEquals("id", "chapter-content")},
	{tag: "div", matchAttr: classToken("chapter-content")},
	{tag: "div", matchAttr: attrEquals("id", "content")},
	{tag: "div", matchAttr: classToken("content")},
	{tag: "div", matchAttr: attrEquals("id", "reading-content")},
	{tag: "div", matchAttr: classToken("reading-content")},
	{tag: "div", matchAttr: attrEquals("id", "article")},
	{tag: "div", matchAttr: classContainsSubstr("chapter")},
	{tag: "div", matchAttr: classToken("txt")},
	{tag: "article", matchAttr: nil},
	{tag: "main", matchAttr: nil},
	{tag: "div", matchAttr: classToken("m-read")},
	{tag: "body", matchAttr: nil},
}

// ─── fetchChapter — المنطق الكامل مع الكاش ─────────────────────

// fetchChapter يجلب فصلاً من Freewebnovel. إن كان explicitSlug غير فارغ
// (حقل novel_id القادم من العميل)، يُستخدم مباشرة كـ slug الرابط بدل
// تخمينه تلقائياً عبر slugify(novelName) — مفيد عندما يعرف العميل الـ slug
// الدقيق مسبقاً (مثلاً من نتيجة بحث سابقة) ويريد تفادي أي التباس.
func (s *Service) fetchChapter(ctx context.Context, novelName, chapterNum, explicitSlug, requestedSite string) (map[string]any, error) {
	selectedSite, ok := normalizeNovelSite(requestedSite)
	if !ok {
		return nil, fmt.Errorf("المصدر %q غير مدعوم حالياً", requestedSite)
	}
	cacheNovelKey := strings.ToLower(novelName)
	if explicitSlug != "" {
		cacheNovelKey = "id:" + strings.ToLower(explicitSlug)
	}
	key := fmt.Sprintf("%s:%s:%s", strings.ToLower(selectedSite), cacheNovelKey, chapterNum)
	if cached, ok := s.cacheGet(key); ok {
		return cached, nil
	}

	slug := explicitSlug
	if slug == "" {
		slug = slugify(novelName)
	}
	if slug == "" {
		return nil, fmt.Errorf("اسم الرواية غير صالح")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(slug) {
		return nil, fmt.Errorf("معرّف الرواية غير صالح")
	}

	var url, htmlStr string
	if selectedSite == novelFullSiteName {
		var err error
		htmlStr, url, err = s.fetchNovelFullChapter(ctx, slug, chapterNum)
		if err != nil {
			return nil, err
		}
	} else {
		url = buildChapterURL(slug, chapterNum)
		log.Printf("[novel] [HTTP] جلب %s", url)
		htmlStr = s.fetchPageHTTP(ctx, url)

		if htmlStr == "" {
			urlHTML := url + ".html"
			log.Printf("[novel] [HTTP] جرب .html: %s", urlHTML)
			htmlStr = s.fetchPageHTTP(ctx, urlHTML)
		}

		if htmlStr == "" {
			log.Printf("[novel] [Browser] جلب %s", url)
			htmlStr = fetchPageBrowser(ctx, url)
		}

		// freewebnovel.com غير متّسق في تسمية الـ slug عبر الروايات: بعضها
		// بلا لاحقة وبعضها بلاحقة -novel. جرّب اللاحقة قبل الاستسلام.
		if htmlStr == "" && explicitSlug == "" && !strings.HasSuffix(slug, "-novel") {
			altSlug := slug + "-novel"
			altURL := buildChapterURL(altSlug, chapterNum)

			log.Printf("[novel] [HTTP] جرب slug بديل: %s", altURL)
			htmlStr = s.fetchPageHTTP(ctx, altURL)
			if htmlStr == "" {
				htmlStr = s.fetchPageHTTP(ctx, altURL+".html")
			}
			if htmlStr == "" {
				log.Printf("[novel] [Browser] جرب slug بديل: %s", altURL)
				htmlStr = fetchPageBrowser(ctx, altURL)
			}
			if htmlStr != "" {
				slug = altSlug
				url = altURL
			}
		}
	}

	if htmlStr == "" {
		return nil, fmt.Errorf("فشل جلب الصفحة بكل الطرق: %s", url)
	}

	paragraphs := extract(htmlStr, contentSelectors)
	if paragraphs == nil {
		sample := htmlStr
		if len(sample) > 1000 {
			sample = sample[:1000]
		}
		log.Printf("[novel] عينة HTML: %s", strings.ReplaceAll(sample, "\n", " "))
		return nil, fmt.Errorf("لم يُعثر على محتوى (تحقق من بنية الصفحة)")
	}

	title := extractTitle(htmlStr)
	if title == "" {
		title = novelName
	}

	wordCount := 0
	for _, p := range paragraphs {
		wordCount += len(strings.Fields(p))
	}

	var chapterValue any = chapterNum
	if integerChapter, err := strconv.Atoi(chapterNum); err == nil {
		chapterValue = integerChapter
	}
	result := map[string]any{
		"title":      title,
		"chapter":    chapterValue,
		"paragraphs": paragraphs,
		"site":       selectedSite,
		"url":        url,
		"word_count": wordCount,
	}
	s.cacheSet(key, result)
	return result, nil
}

// ─── HTTP handlers ───────────────────────────────────────────────

func (s *Service) handleGetChapter(r *http.Request) (map[string]any, error) {
	var body struct {
		Novel   string `json:"novel"`
		Chapter any    `json:"chapter"`
		Site    string `json:"site"`     // اختياري — Freewebnovel افتراضي، أو NovelFull
		NovelID string `json:"novel_id"` // اختياري — نفس الشيء، يُستخدم الآن كـ slug صريح
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": strutil.Truncate(err.Error(), 200)},
		}
	}

	requestedSite, siteOK := normalizeNovelSite(body.Site)
	if !siteOK {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{
				"error":           fmt.Sprintf("المصدر %q غير مدعوم حالياً", strings.TrimSpace(body.Site)),
				"supported_sites": []string{siteName, novelFullSiteName},
			},
		}
	}

	novelName := strings.TrimSpace(body.Novel)
	novelID := strings.TrimSpace(body.NovelID)
	if novelName == "" && novelID == "" {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "novel أو novel_id مطلوب"},
		}
	}

	chapterNum, chapterOK := parseChapterNumber(body.Chapter)
	if !chapterOK {
		return nil, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"error": "chapter رقم موجب صحيح أو عشري"},
		}
	}

	result, err := s.fetchChapter(r.Context(), novelName, chapterNum, novelID, requestedSite)
	if err != nil {
		return nil, &httpx.HTTPError{
			Code: http.StatusNotFound,
			Body: map[string]any{"error": err.Error()},
		}
	}
	return result, nil
}

func (s *Service) handleListSites(r *http.Request) (map[string]any, error) {
	return map[string]any{"sites": []string{siteName, novelFullSiteName}}, nil
}

func (s *Service) handleClearCache(r *http.Request) (map[string]any, error) {
	n := s.cacheClear()
	return map[string]any{"cleared": n}, nil
}
