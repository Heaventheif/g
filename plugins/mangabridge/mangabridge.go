// Package mangabridge يقابل plugins/manga_bridge.py: endpoints الـ job
// (إنشاء/حالة/صور) + الكشط الفعلي. الكشط (كان عبر Node.js/Playwright في
// scripts/mangabridge/manga_scraper.js، المحذوف الآن) صار هنا Go خالصاً
// 100%: chromedp يتحدث مباشرة مع Chrome عبر CDP — راجع internal/browser
// للبنية التحتية المشتركة مع plugins/pinterest (نفس منطق التشغيل بالضبط).
//
// فرق تصميم متعمّد عن بايثون: بدل SQLite (يحتاج إما cgo مع
// mattn/go-sqlite3، أو محرّك Go نقي من modernc.org — نطاق محجوب هنا
// أيضاً بنفس قيد golang.org الموثّق سابقاً)، استخدمنا نفس نمط مخزن
// الجلسات (internal/session): خريطة في الذاكرة محمية بـ mutex — لكن هنا
// كحقلَي Service بدل متغيرات حزمة عامة (راجع §2.2). النتيجة نفس السلوك
// الوظيفي (بيانات job قصيرة العمر، TTL ساعة واحدة) بدون أي تبعية خارجية.
package mangabridge

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/plugins"
)

const Description = "كشط فصول المانجا — Go خالص (chromedp/CDP) + بنية job كاملة"

const (
	jobTTL             = time.Hour
	imagesSubdir       = "data/manga_bridge/images"
	maxMangaImages     = 300
	maxMangaImageBytes = 16 * 1024 * 1024
)

// Service يحمل حالة الـ jobs في الذاكرة كحقلين صريحين (بدل jobsMu/jobs
// كمتغيرات حزمة عامة سابقاً) — هذا بالضبط النمط الممنوع صراحة في القيود
// الصارمة (map[string]*job على مستوى الحزمة)، والسبب الرئيسي لوجود هذا
// الـ Service أصلاً.
// حارس التزامن (sem): يحد عدد نسخ Chromium المشغّلة في آن واحد — كل
// واحدة ثقيلة بالذاكرة، وعدة طلبات متزامنة قد تُسقط العملية كلها بدون
// هذا الحد على حاوية بموارد محدودة.
type Service struct {
	jobsMu sync.Mutex
	jobs   map[string]*mangaJob

	sem chan struct{}
}

// concurrentScrapes: حد أقصى لعدد عمليات الكشط (Chromium) المتزامنة.
// اضبطه حسب الذاكرة المتاحة فعلياً في بيئة الإنتاج.
const concurrentScrapes = 2

func New() *Service {
	return &Service{
		jobs: map[string]*mangaJob{},
		sem:  make(chan struct{}, concurrentScrapes),
	}
}

func (s *Service) Name() string { return "manga_bridge" }

func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "POST", Pattern: "/manga-bridge/jobs", Handler: httpx.WrapJSONLimit(maxCreateJobBodyBytes, s.handleCreateJob)},
		{Method: "GET", Pattern: "/manga-bridge/jobs/{job_id}", Handler: httpx.Handle(s.handleGetJob)},
		{Method: "GET", Pattern: "/manga-bridge/jobs/{job_id}/image/{idx}", Handler: s.handleGetJobImage},
	}
}

// ─── HTTP handlers ───────────────────────────────────────────────

// maxCreateJobBodyBytes: جسم الطلب اسم (≤120) + رقم فصل فقط، فـ 16KB سخي جداً.
const maxCreateJobBodyBytes = 16 << 10

type createJobBody struct {
	Manga   string `json:"manga"`
	Chapter string `json:"chapter"`
}

type createJobResult struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

func (s *Service) handleCreateJob(_ context.Context, body createJobBody) (createJobResult, error) {
	s.cleanupOldJobs()

	manga := strings.TrimSpace(body.Manga)
	chapter := strings.TrimSpace(body.Chapter)
	if manga == "" || len(manga) > 120 || !validChapterNumber(chapter) {
		return createJobResult{}, &httpx.HTTPError{
			Code: http.StatusBadRequest,
			Body: map[string]any{"detail": "أدخل اسماً لا يتجاوز 120 حرفاً ورقم فصل موجباً مثل 12 أو 12.5"},
		}
	}

	j := s.createJob(manga, chapter)
	log.Printf("[manga-bridge] job جديدة %s | %s #%s", j.ID, manga, chapter)

	go s.runJob(j.ID, manga, chapter)

	return createJobResult{JobID: j.ID, Status: "pending"}, nil
}

type jobStatusResult struct {
	Status       jobStatus `json:"status"`
	ChapterTitle string    `json:"chapter_title"`
	ChapterURL   string    `json:"chapter_url"`
	ImageCount   int       `json:"image_count"`
	Error        string    `json:"error"`
}

func (s *Service) handleGetJob(r *http.Request) (jobStatusResult, error) {
	jobID := r.PathValue("job_id")
	j, ok := s.getJob(jobID)
	if !ok {
		return jobStatusResult{}, &httpx.HTTPError{
			Code: http.StatusNotFound,
			Body: map[string]any{"detail": "job غير موجودة (ممكن تكون انتهت صلاحيتها)"},
		}
	}
	return jobStatusResult{
		Status:       j.Status,
		ChapterTitle: j.ChapterTitle,
		ChapterURL:   j.ChapterURL,
		ImageCount:   j.ImageCount,
		Error:        j.Error,
	}, nil
}

// handleGetJobImage يخدم ملف الصورة مباشرة (http.ServeFile) — لا يلائم
// شكل httpx.Handle[Res] العام (لا يوجد جسم JSON للرد الناجح)، لذا يبقى
// http.HandlerFunc عادياً يُمرَّر مباشرة في Routes()، تماماً كما يسمح به
// نوع Route.Handler (راجع internal/plugins).
func (s *Service) handleGetJobImage(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	idxStr := r.PathValue("idx")
	if !jobIDRe.MatchString(jobID) {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"detail": "job_id غير صالح"})
		return
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"detail": "idx غير صالح"})
		return
	}

	for _, ext := range []string{".jpg", ".png", ".gif", ".webp", ".avif", ".bmp", ".tif", ".img"} {
		imagePath := filepath.Join(imagesSubdir, jobID, fmt.Sprintf("%d%s", idx, ext))
		if _, err := os.Stat(imagePath); err == nil {
			http.ServeFile(w, r, imagePath)
			return
		}
	}
	httpx.JSON(w, http.StatusNotFound, map[string]any{"detail": "الصورة غير موجودة"})
}
