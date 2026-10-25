// Package httpx يوفّر البدائية العامة الوحيدة لكل الـ handlers في هذا
// المشروع (Handle)، وسكّراً نحوياً فوقها لخدمات JSON in/out البسيطة
// (WrapJSON) — إلزامي للخدمات الجديدة فقط، غير رجعي على التسعة القديمة.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

// HTTPError يحمل جسم الاستجابة الكامل (وليس رسالة نصية فقط) لأن شكل جسم
// الخطأ يختلف فعلياً بين plugin وآخر في هذا المشروع (بعضها "error" وبعضها
// "detail"، وبعضها برسالة الخطأ الفعلية وبعضها برسالة ثابتة).
type HTTPError struct {
	Code int
	Body any // أي قيمة قابلة لـ json.Marshal — تُرسَل كما هي، حرفياً
}

func (e *HTTPError) Error() string {
	b, _ := json.Marshal(e.Body)
	return string(b)
}

func JSON[T any](w http.ResponseWriter, status int, payload T) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

// DefaultMaxBodyBytes: الحد الافتراضي لجسم JSON في Decode/WrapJSON. المسارات التي
// تحمل بيانات ثنائية مضمّنة (base64: صوت/صور) تستخدم DecodeLimit/WrapJSONLimit
// بحد أكبر صريح، والمسارات الصغيرة جداً بحد أصغر.
const DefaultMaxBodyBytes int64 = 10 << 20

// Decode يفكّ جسم JSON بحد DefaultMaxBodyBytes.
func Decode[T any](r *http.Request) (T, error) {
	return DecodeLimit[T](r, DefaultMaxBodyBytes)
}

// DecodeLimit يفكّ جسم JSON ويقرأ على الأكثر maxBytes (+1 للكشف عن التجاوز) —
// الجسم الأكبر يُقطع بدل أن يُحمَّل كاملاً في الذاكرة. استُخدم w=nil عمداً في
// MaxBytesReader: Handle لا يمرّر ResponseWriter للـ handlers؛ والمكتبة تتحمّل nil
// (تُهمل فقط ضبط Connection: close). تجاوز الحد يُرجع *http.MaxBytesError (راجع IsTooLarge).
func DecodeLimit[T any](r *http.Request, maxBytes int64) (T, error) {
	var v T
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	err := json.NewDecoder(r.Body).Decode(&v)
	return v, err
}

// IsTooLarge يُخبر إن كان الخطأ ناتجاً عن تجاوز حد حجم الجسم.
func IsTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// Handle هي البدائية الوحيدة والعامة لكل route — تعمل بلا فرق لأي شكل:
// GET بلا جسم، POST بجسم JSON، أو مسار بـ path parameters — لأنها تمرّر
// *http.Request كاملاً لدالة الـ handler، ولا تفترض شيئاً مسبقاً عن الجسم
// أو المسار. بدائية واحدة تغطي كل الحالات، بلا ازدواجية "Wrap أحياناً /
// handler عادي أحياناً".
func Handle[Res any](h func(r *http.Request) (Res, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		res, err := h(r)
		elapsed := time.Since(start)

		if err != nil {
			var he *HTTPError
			if errors.As(err, &he) {
				log.Printf("[%s %s] %d (%s) — %s", r.Method, r.URL.Path, he.Code, elapsed, he.Error())
				JSON(w, he.Code, he.Body)
				return
			}
			log.Printf("[%s %s] %d (%s) — %s", r.Method, r.URL.Path, http.StatusInternalServerError, elapsed, err.Error())
			Error(w, http.StatusInternalServerError, "خطأ داخلي، حاول لاحقاً")
			return
		}
		log.Printf("[%s %s] %d (%s)", r.Method, r.URL.Path, http.StatusOK, elapsed)
		JSON(w, http.StatusOK, res)
	}
}

// WrapJSON سكّر نحوي فوق Handle — إلزامي للخدمات الجديدة فقط لأي endpoint
// بجسم JSON. كل خدمة جديدة تُضاف للمشروع من الآن فصاعداً تستخدم WrapJSON
// لأي endpoint بجسم JSON، وHandle مباشرة لأي endpoint بلا جسم أو بحاجة
// لـ path parameters. لا مجال بعد الآن لاختلافات "error" مقابل "detail"
// أو 400 مقابل 500 كما حدث تاريخياً بين التسعة القدامى — تلك الاختلافات
// مجمَّدة فقط في القديم (راجع الخدمات المرحَّلة)، ومحظورة في أي شيء جديد.
func WrapJSON[Req, Res any](h func(ctx context.Context, req Req) (Res, error)) http.HandlerFunc {
	return WrapJSONLimit(DefaultMaxBodyBytes, h)
}

// WrapJSONLimit مثل WrapJSON بحد صريح لحجم الجسم (413 عند التجاوز، 400 لغير ذلك).
func WrapJSONLimit[Req, Res any](maxBytes int64, h func(ctx context.Context, req Req) (Res, error)) http.HandlerFunc {
	return Handle(func(r *http.Request) (Res, error) {
		req, err := DecodeLimit[Req](r, maxBytes)
		if err != nil {
			var zero Res
			if IsTooLarge(err) {
				return zero, &HTTPError{
					Code: http.StatusRequestEntityTooLarge,
					Body: map[string]string{"error": "request body too large"},
				}
			}
			return zero, &HTTPError{
				Code: http.StatusBadRequest,
				Body: map[string]string{"error": "invalid request body"},
			}
		}
		return h(r.Context(), req)
	})
}
