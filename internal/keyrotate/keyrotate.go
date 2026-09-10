// Package keyrotate يدير مجموعة مفاتيح API (مثلاً عدة مفاتيح Groq/Gemini/Ferdev)
// مخزّنة في متغير بيئة واحد مفصول بفواصل (key1,key2,key3)، ويوفّر إستراتيجيتين
// معاً بأمان بين الخيوط (thread-safe عبر sync/atomic):
//
//   - Round-Robin: كل طلب مستقل يبدأ من مفتاح مختلف — يوزّع الحمل بالتساوي.
//   - Rate-Limit Fallback: عند 429 يُبدَّل فوراً للمفتاح التالي ويُعاد
//     المحاولة، ضمن نفس الطلب.
//
// الاستخدام النموذجي:
//
//	var groqKeys = sync.OnceValue(func() *keyrotate.Manager {
//		return keyrotate.FromEnv("GROQ_API_KEY")
//	})
//
//	var reply string
//	err := groqKeys().Do(func(key string) (rateLimited bool, err error) {
//		r, status, e := callAPI(key)
//		if e != nil {
//			return status == http.StatusTooManyRequests, e
//		}
//		reply = r
//		return false, nil
//	})
package keyrotate

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// ErrNoKeys تُعاد عندما لا توجد أي مفاتيح مضبوطة في المتغيرات البيئية.
var ErrNoKeys = errors.New("لا توجد مفاتيح API مضبوطة")

// Manager يحمل مجموعة مفاتيح ثابتة (لا تتغيّر بعد الإنشاء) مع مؤشر تدوير
// آمن بين الخيوط. القيمة الصفرية غير صالحة — يجب إنشاؤه عبر New/FromEnv/FromEnvAny.
type Manager struct {
	keys []string
	idx  uint32
}

// New يبني Manager من نص واحد مفصول بفواصل (مثل "key1, key2 ,key3").
// المسافات حول كل مفتاح تُزال، والمفاتيح الفارغة تُتجاهل.
func New(raw string) *Manager {
	return &Manager{keys: splitClean(raw)}
}

// FromEnv يبني Manager مباشرة من متغير بيئة واحد (قائمة مفاتيح مفصولة بفواصل).
func FromEnv(name string) *Manager { return New(os.Getenv(name)) }

// FromEnvAny يدمج عدة متغيرات بيئة في مجموعة مفاتيح واحدة — كل متغير قد
// يحوي مفتاحاً واحداً أو عدة مفاتيح مفصولة بفواصل. مفيد للتوافق الخلفي مع
// أنماط مثل GEMINI_API_KEY, GEMINI_API_KEY_2, GEMINI_API_KEY_3...
func FromEnvAny(names ...string) *Manager {
	var all []string
	for _, n := range names {
		all = append(all, splitClean(os.Getenv(n))...)
	}
	return &Manager{keys: all}
}

func splitClean(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if k := strings.TrimSpace(p); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Len يُرجع عدد المفاتيح المتاحة.
func (m *Manager) Len() int { return len(m.keys) }

// Empty يُرجع true إن لم تُضبط أي مفاتيح.
func (m *Manager) Empty() bool { return len(m.keys) == 0 }

// Keys يُرجع نسخة من قائمة المفاتيح كما هي (بدون تدوير) — للتشخيص فقط،
// لا تُعدِّل الشريحة المُرجعة.
func (m *Manager) Keys() []string { return m.keys }

// Next يُرجع المفتاح التالي بالتناوب (إستراتيجية Round-Robin) بأمان بين
// الخيوط عبر sync/atomic، ويُحرِّك المؤشر المشترك خطوة واحدة إلى الأمام.
// مناسبة لتوزيع مفتاح مختلف على كل طلب مستقل (مثل TTS/طلب بسيط بلا Retry).
func (m *Manager) Next() string {
	n := uint32(len(m.keys))
	if n == 0 {
		return ""
	}
	i := atomic.AddUint32(&m.idx, 1) - 1
	return m.keys[i%n]
}

// RotatedKeys يُرجع كل المفاتيح بترتيب دائري يبدأ من نقطة تدوير مختلفة مع
// كل استدعاء (نفس منطق Next لكن يُرجع القائمة كاملة بدل مفتاح واحد)، بحيث
// يتوزّع "المفتاح الأساسي" لكل طلب على التساوي، مع إبقاء باقي المفاتيح
// متاحة كخطة احتياط (fallback) ضمن نفس الطلب عند الحاجة.
func (m *Manager) RotatedKeys() []string {
	n := uint32(len(m.keys))
	if n == 0 {
		return nil
	}
	start := (atomic.AddUint32(&m.idx, 1) - 1) % n
	out := make([]string, n)
	for i := uint32(0); i < n; i++ {
		out[i] = m.keys[(start+i)%n]
	}
	return out
}

// Do ينفّذ fn مرة لكل مفتاح بترتيب RotatedKeys حتى ينجح أحدها (err == nil)،
// ويجمع بين الإستراتيجيتين: يبدأ من مفتاح مختلف كل مرة (Round-Robin) ثم
// يتبدّل تلقائياً للمفتاح التالي فقط عند rateLimited == true (429)، ويتوقف
// فوراً عن أي محاولات أخرى لأي خطأ آخر غير متعلق بحد الطلبات.
//
// fn تُستدعى بمفتاح واحد وتُرجع:
//   - rateLimited: true لو كان سبب الفشل تحديداً 429 (جرّب المفتاح التالي).
//   - err: الخطأ نفسه (أو nil عند النجاح).
func (m *Manager) Do(fn func(key string) (rateLimited bool, err error)) error {
	keys := m.RotatedKeys()
	if len(keys) == 0 {
		return ErrNoKeys
	}
	var lastErr error
	for _, key := range keys {
		limited, err := fn(key)
		if err == nil {
			return nil
		}
		lastErr = err
		if !limited {
			return err
		}
	}
	return fmt.Errorf("استُنفدت كل المفاتيح (%d) بسبب حد الطلبات (429): %w", len(keys), lastErr)
}
