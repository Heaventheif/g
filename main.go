// main.go هو الملف الوحيد الذي "يعرف" عن كل الخدمات — كل خدمة أخرى
// معزولة تماماً عن غيرها ولا تعرف بوجودها. إضافة خدمة جديدة = حزمة جديدة
// تحت plugins/ تلتزم بعقد plugins.Service + سطر واحد في services() أدناه.
// لا registry، لا init()، لا blank imports — الأخطاء (مثل نسيان تسجيل
// خدمة، وهو تحديداً ما حدث سابقاً مع img_tr) تُكتشف الآن وقت الترجمة:
// نسيان سطر هنا يعني ببساطة أن الخدمة غير موجودة في القائمة، لا عطلاً
// صامتاً وقت التشغيل.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/middleware"
	"sunkenbot/internal/plugins"
	"sunkenbot/internal/session"
	"sunkenbot/internal/shared"

	"sunkenbot/plugins/chess"
	"sunkenbot/plugins/comic"
	"sunkenbot/plugins/gemini"
	"sunkenbot/plugins/groq"
	"sunkenbot/plugins/mangabridge"
	"sunkenbot/plugins/novel"
	"sunkenbot/plugins/ping"
	"sunkenbot/plugins/pinterest"
)

const defaultPort = "7860"

// descriptions يقابل وصف كل خدمة (كان description في registry.Result
// سابقاً) — يبقى منفصلاً عن عقد plugins.Service عمداً (الواجهة تحتوي فقط
// Name/Routes، راجع internal/plugins) بدل توسيعها بميثود Description()
// لخدمة عرض بحت في endpoint واحد.
var descriptions = map[string]string{
	"chess":        chess.Description,
	"comic":        comic.Description,
	"gemini":       gemini.Description,
	"groq":         groq.Description,
	"manga_bridge": mangabridge.Description,
	"novel":        novel.Description,
	"ping":         ping.Description,
	"pinterest":    pinterest.Description,
}

// services يبني كل الخدمات باعتمادياتها الصريحة. هذا الموضع — وليس كل
// حزمة plugin بمفردها — هو المكان الوحيد الذي يقرر أي http.Client يُمرَّر
// لأي خدمة (راجع جدول قرار http.Client في برومبت الترحيل لسبب كل
// اختيار: shared.Client لكل عميل كانت مهلته 30 ثانية أصلاً، وعميل محلي
// صريح لأي مهلة مختلفة — بما فيها "بلا مهلة إطلاقاً" لـ novel).
func services() []plugins.Service {
	geminiClient := &http.Client{Timeout: 60 * time.Second}
	groqDL := &http.Client{Timeout: 120 * time.Second}
	novelClient := &http.Client{} // بلا مهلة عامة — كل طلب يضبط مهلته عبر context، نفس السلوك القديم حرفياً
	comicClient := &http.Client{Timeout: 20 * time.Second}

	return []plugins.Service{
		chess.New(),
		comic.New(comicClient),
		gemini.New(geminiClient, session.New("gemini_sessions")),
		groq.New(shared.Client, groqDL, session.New("groq_sessions")),
		mangabridge.New(),
		novel.New(novelClient),
		pinterest.New(shared.Client),
		ping.New(),
	}
}

// checkDescriptions يتأكد أن لكل خدمة مسجّلة وصفاً في descriptions — نسيانه
// كان يمرّ بصمت (وصف فارغ في GET /) بينما نسيان سطر في services() يُكتشف
// وقت الترجمة؛ هذا الفحص يغلق الفجوة الثانية بفشل بدء تشغيل واضح.
func checkDescriptions(svcs []plugins.Service, descs map[string]string) error {
	var missing []string
	for _, svc := range svcs {
		if descs[svc.Name()] == "" {
			missing = append(missing, svc.Name())
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("خدمات بلا وصف في descriptions: %s", strings.Join(missing, ", "))
	}
	return nil
}

// checkAuthConfig: REQUIRE_AUTH=1 يجعل غياب INTERNAL_TOKEN خطأ بدء تشغيل بدل
// تشغيل كل الـ endpoints مفتوحة مع تحذير فقط. بدون REQUIRE_AUTH السلوك القديم.
func checkAuthConfig(requireAuth, internalToken string) error {
	if strings.TrimSpace(requireAuth) == "1" && strings.TrimSpace(internalToken) == "" {
		return fmt.Errorf("REQUIRE_AUTH=1 لكن INTERNAL_TOKEN غير مضبوط — ارفض التشغيل بلا حماية")
	}
	return nil
}

// register يسجّل مسارات كل الخدمات في mux ويعيد ملخصاً لها (لـ GET /).
func register(mux *http.ServeMux, svcs []plugins.Service) map[string]any {
	results := make(map[string]any, len(svcs))
	for _, svc := range svcs {
		routes := svc.Routes()
		for _, rt := range routes {
			mux.HandleFunc(rt.Method+" "+rt.Pattern, rt.Handler)
		}
		results[svc.Name()] = map[string]any{
			"status":       "loaded",
			"routes_added": len(routes),
			"description":  descriptions[svc.Name()],
		}
		log.Printf("[%s] ✅ محمَّل — %d route(s) مضافة", svc.Name(), len(routes))
	}
	return results
}

// newHandler يبني المعالج الكامل: المسارات العامة + الخدمات + CORS + حماية التوكن.
func newHandler(svcs []plugins.Service, internalToken string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", httpx.Handle(func(r *http.Request) (map[string]any, error) {
		return map[string]any{"status": "healthy", "timestamp": time.Now().Unix()}, nil
	}))

	pluginResults := register(mux, svcs)
	log.Printf("[main] ✅ تم تحميل %d خدمة", len(pluginResults))

	mux.HandleFunc("GET /{$}", httpx.Handle(func(r *http.Request) (map[string]any, error) {
		return map[string]any{"status": "online", "plugins": pluginResults}, nil
	}))

	// طبقات الـ middleware: CORS ثم حماية التوكن
	var handler http.Handler = mux
	handler = middleware.Auth(internalToken, handler)
	return middleware.CORS(handler)
}

// listenPort يقرأ PORT ويعود للافتراضي إن كان فارغاً أو غير رقمي.
func listenPort() string {
	port := os.Getenv("PORT")
	if _, err := strconv.Atoi(port); err != nil {
		return defaultPort
	}
	return port
}

// shutdownTimeout: المهلة الممنوحة للطلبات الجارية عند الإيقاف.
const shutdownTimeout = 20 * time.Second

func main() {
	log.SetFlags(log.LstdFlags)

	internalToken := os.Getenv("INTERNAL_TOKEN")
	if err := checkAuthConfig(os.Getenv("REQUIRE_AUTH"), internalToken); err != nil {
		log.Fatal(err)
	}
	svcs := services()
	if err := checkDescriptions(svcs, descriptions); err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:    ":" + listenPort(),
		Handler: newHandler(svcs, internalToken),
		// حماية من الاتصالات البطيئة. لا WriteTimeout عمداً: بعض المسارات
		// (STT/TTS/novel) قد تستغرق دقائق، ومهلتها تُضبط لكل طلب عبر context.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// إيقاف سليم عند SIGTERM/SIGINT (إعادة نشر HF): ننهي الطلبات الجارية ثم نُغلق
	// الخدمات التي تملك موارد (مثل عمليات Stockfish) عبر io.Closer الاختياري.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("🚀 Sunken Bot (Go) يستمع على %s", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		log.Fatal(err) // فشل الاستماع (منفذ مشغول…)
	case <-ctx.Done():
		log.Printf("[main] ⏹ استلام إشارة إيقاف — إنهاء الطلبات الجارية…")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("[main] ⚠️ الإيقاف لم يكتمل ضمن المهلة: %v", err)
	}
	for _, svc := range svcs {
		if c, ok := svc.(io.Closer); ok {
			if err := c.Close(); err != nil {
				log.Printf("[%s] ⚠️ خطأ عند الإغلاق: %v", svc.Name(), err)
			}
		}
	}
	log.Printf("[main] ✅ تم الإيقاف")
}
