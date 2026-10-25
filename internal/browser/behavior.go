package browser

// behavior.go — سلوك بشري: انتظار عشوائي وحركة فأرة.

import (
	"context"
	"math/rand"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

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
		time.Sleep(time.Duration(12+rand.Intn(25)) * time.Millisecond)
	}
	return nil
}
