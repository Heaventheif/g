package mangabridge

// job.go — مهام التحميل: الحالة والتخزين والتنظيف.

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"sunkenbot/internal/strutil"
)

// ─── حالة الـ job في الذاكرة (تقابل جدول jobs في SQLite) ────────────

type jobStatus string

const (
	statusPending jobStatus = "pending"
	statusRunning jobStatus = "running"
	statusDone    jobStatus = "done"
	statusError   jobStatus = "error"
)

type mangaJob struct {
	ID           string
	Manga        string
	Chapter      string
	Status       jobStatus
	ChapterTitle string
	ChapterURL   string
	ImageCount   int
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (s *Service) createJob(manga, chapter string) *mangaJob {
	id := randomHex(jobIDLen)
	now := time.Now()
	j := &mangaJob{ID: id, Manga: manga, Chapter: chapter, Status: statusPending, CreatedAt: now, UpdatedAt: now}
	s.jobsMu.Lock()
	s.jobs[id] = j
	s.jobsMu.Unlock()
	return j
}

func (s *Service) getJob(id string) (*mangaJob, bool) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

func (s *Service) updateJob(id string, mutate func(*mangaJob)) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if j, ok := s.jobs[id]; ok {
		mutate(j)
		j.UpdatedAt = time.Now()
	}
}

// cleanupOldJobs يقابل _cleanup_old_jobs في بايثون — يمسح الـ jobs
// وصورها الأقدم من JOB_TTL_SECONDS، يُستدعى مع كل job جديدة.
func (s *Service) cleanupOldJobs() {
	cutoff := time.Now().Add(-jobTTL)
	s.jobsMu.Lock()
	var expired []string
	for id, j := range s.jobs {
		if j.CreatedAt.Before(cutoff) {
			expired = append(expired, id)
			delete(s.jobs, id)
		}
	}
	s.jobsMu.Unlock()

	for _, id := range expired {
		_ = os.RemoveAll(filepath.Join(imagesSubdir, id))
	}
}

// ─── تشغيل الـ job في الخلفية (goroutine تقابل BackgroundTasks) ─────

func (s *Service) runJob(jobID, manga, chapter string) {
	s.updateJob(jobID, func(j *mangaJob) { j.Status = statusRunning })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	jobDir := filepath.Join(imagesSubdir, jobID)
	title, u, imageCount, err := s.scrapeChapter(ctx, manga, chapter, jobDir)
	if err != nil || imageCount == 0 {
		reason := "لم يتم العثور على صور — إما اسم/فصل غير صحيح، أو تحدي Cloudflare لم يُحل من IP الخادم."
		if err != nil {
			reason = err.Error()
		}
		s.updateJob(jobID, func(j *mangaJob) {
			j.Status = statusError
			j.Error = strutil.Truncate(reason, 500)
		})
		return
	}

	s.updateJob(jobID, func(j *mangaJob) {
		j.Status = statusDone
		j.ChapterTitle = title
		j.ChapterURL = u
		j.ImageCount = imageCount
	})
	log.Printf("[manga-bridge] job %s اكتملت — %d صورة", jobID, imageCount)
}

// ─── أدوات مساعدة ──────────────────────────────────────────────────

// jobIDLen طول معرّف الـ job بالأحرف السداسية — يجب أن يطابق jobIDRe
// ({32}) في validation.go (اختبار الانحدار يتحقق من التطابق).
const jobIDLen = 32

// randomHex يُرجع n حرفاً سداسياً بالضبط.
func randomHex(n int) string {
	buf := make([]byte, n/2+1)
	if _, err := cryptorand.Read(buf); err != nil {
		// احتياط شديد الندرة فقط لو فشل مصدر العشوائية بالنظام.
		// UnixNano يعطي ~16 حرفاً فقط، فنكرّره/نقصّه ليبلغ n بالضبط
		// (التقطيع المباشر [:n] كان سيُسبب panic عند n=32).
		out := ""
		for len(out) < n {
			out += fmt.Sprintf("%016x", time.Now().UnixNano())
		}
		return out[:n]
	}
	return hex.EncodeToString(buf)[:n]
}
