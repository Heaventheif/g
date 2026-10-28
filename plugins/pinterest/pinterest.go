// Package pinterest يقابل plugins/pinterest.py: بحث وتحميل صور Pinterest
// عالية الدقة. الكشط الفعلي (كان عبر Playwright في Node.js —
// scripts/pinterest/pinterest_scraper.js، المحذوف الآن) صار هنا Go
// خالصاً 100%: chromedp يتحدث مباشرة مع Chrome عبر بروتوكول Chrome
// DevTools (CDP) بلا أي وسيط Node/npm — راجع internal/browser للبنية
// التحتية المشتركة مع plugins/mangabridge (نفس منطق التشغيل بالضبط).
package pinterest

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/plugins"
)

const Description = "بحث وتحميل صور عالية الدقة من Pinterest (chromedp/CDP خالص Go + Ferdev fallback)"

// Service يحمل عميل http.Client (المشترك بمهلة 30 ثانية — راجع جدول قرار
// http.Client) وحالة health-check البسيطة (healthMu/lastScraperRan —
// كانت متغيرات حزمة عامة، انتقلت هنا لنفس سبب انتقال jobsMu في
// mangabridge) كحقول Service صريحة.
type Service struct {
	client *http.Client

	healthMu       sync.Mutex
	lastScraperRan bool

	// حارس التزامن: يحد عدد نسخ Chromium (عبر chromedp/CDP مباشرة الآن،
	// بدل Node/Playwright سابقاً) المشغّلة في آن واحد — راجع نفس المنطق
	// في plugins/mangabridge.
	sem chan struct{}
}

// concurrentScrapes: حد أقصى لعدد عمليات الكشط (Chromium) المتزامنة.
const concurrentScrapes = 2

func New(client *http.Client) *Service {
	return &Service{client: client, sem: make(chan struct{}, concurrentScrapes)}
}

func (s *Service) Name() string { return "pinterest" }

func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "POST", Pattern: "/pinterest", Handler: httpx.WrapJSON(s.handleSearch)},
		{Method: "GET", Pattern: "/pinterest/health", Handler: httpx.Handle(s.handleHealth)},
	}
}

// ─── أنواع الطلب/الرد (تطابق PinterestRequest/PinterestResponse) ────

type pinterestRequest struct {
	Query          string `json:"query"`
	Limit          int    `json:"limit"`
	Quality        string `json:"quality"`
	Username       string `json:"username"`
	BoardSlug      string `json:"board_slug"`
	AsBase64       bool   `json:"as_base64"`
	FallbackFerdev *bool  `json:"fallback_ferdev"` // مؤشر لتمييز "لم يُرسَل" عن false صراحة
}

type imageResult struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Width     *int   `json:"width,omitempty"`
	Height    *int   `json:"height,omitempty"`
}

type pinterestResponse struct {
	Success   bool          `json:"success"`
	Query     string        `json:"query,omitempty"`
	Count     int           `json:"count"`
	Images    []imageResult `json:"images"`
	Provider  string        `json:"provider"`
	Error     string        `json:"error,omitempty"`
	ElapsedMs int64         `json:"elapsed_ms"`
}

// ─── HTTP handlers ───────────────────────────────────────────────

func (s *Service) handleHealth(r *http.Request) (map[string]any, error) {
	s.healthMu.Lock()
	ran := s.lastScraperRan
	s.healthMu.Unlock()

	return map[string]any{
		"ok":                  true,
		"browser_initialised": ran, // true فقط لو نُفِّذ الكشط من قبل بنجاح
		"ferdev_key_present":  !ferdevKeys().Empty(),
		"ferdev_key_count":    ferdevKeys().Len(),
	}, nil
}

func (s *Service) handleSearch(ctx context.Context, req pinterestRequest) (pinterestResponse, error) {
	t0 := time.Now()
	if strings.TrimSpace(req.Query) == "" && req.Username == "" {
		return pinterestResponse{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"detail": "query مطلوب (أو username+board_slug)"},
		}
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}
	if req.Limit > 20 {
		req.Limit = 20
	}
	if req.Quality == "" {
		req.Quality = "original"
	}
	fallbackFerdev := true
	if req.FallbackFerdev != nil {
		fallbackFerdev = *req.FallbackFerdev
	}

	provider := "chromedp"
	var images []imageResult
	var scraperErr string

	pins, err := s.runScraper(ctx, req)
	if err != nil {
		provider = "chromedp-error"
		scraperErr = err.Error()
	} else {
		s.healthMu.Lock()
		s.lastScraperRan = true
		s.healthMu.Unlock()
		if len(pins) > req.Limit {
			pins = pins[:req.Limit]
		}
		images = pins
	}

	// ─── fallback إلى Ferdev ─────────────────────────────────────
	isBoardMode := req.Username != "" && req.BoardSlug != ""
	if len(images) == 0 && fallbackFerdev && !isBoardMode {
		if ferdevImgs := s.ferdevSearch(ctx, req.Query, req.Limit); len(ferdevImgs) > 0 {
			images = ferdevImgs
			provider = "ferdev"
			scraperErr = ""
		}
	}

	// ─── ترميز base64 اختياري (نفس التقصير b64[:120]+"..." في بايثون) ─
	if req.AsBase64 && len(images) > 0 {
		s.encodeThumbnailsBase64(ctx, images)
	}

	if len(images) == 0 && scraperErr == "" {
		scraperErr = "no images found"
	}

	return pinterestResponse{
		Success:   len(images) > 0,
		Query:     req.Query,
		Count:     len(images),
		Images:    images,
		Provider:  provider,
		Error:     scraperErr,
		ElapsedMs: time.Since(t0).Milliseconds(),
	}, nil
}

// ─── أدوات مساعدة ──────────────────────────────────────────────────
