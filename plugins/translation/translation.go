// Package translation يضيف رسائل واجهة ثنائية اللغة وترجمة نص حر.
// يستخدم Fiber للمسارات، ثم يكيّف التطبيق إلى net/http ليتوافق مع بوابة المشروع.
package translation

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	gtranslate "github.com/dinhcanh303/go_translate"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	translator "gopkg.gilang.dev/translator/v2"
	"sunkenbot/internal/plugins"
)

//go:embed locales/active.*.toml
var localeFiles embed.FS

const (
	Description = "ترجمة واجهة المستخدم إلى العربية والإنجليزية وترجمة النصوص بلغات متعددة"
	maxBodySize = 1 << 20
)

type Service struct {
	app       *fiber.App
	fiberHTTP http.HandlerFunc
	bundle    *i18n.Bundle
	backends  map[string]translator.Translator
}

// New يحمّل الرسائل ويجهّز محركي الترجمة مرة واحدة عند بدء التشغيل.
func New() *Service {
	bundle, err := loadBundle()
	if err != nil {
		// ملفات الرسائل مضمّنة وقت البناء؛ وجود خطأ فيها يعني أن الإصدار غير صالح.
		panic(fmt.Errorf("تهيئة خدمة الترجمة: %w", err))
	}
	goTranslateGoogle, err := newGoTranslateBackend(gtranslate.ProviderGoogle)
	if err != nil {
		panic(fmt.Errorf("تهيئة go-translate/Google: %w", err))
	}
	goTranslateMicrosoft, err := newGoTranslateBackend(gtranslate.ProviderMicrosoft)
	if err != nil {
		panic(fmt.Errorf("تهيئة go-translate/Microsoft: %w", err))
	}
	return newService(bundle, map[string]translator.Translator{
		"google":       translator.NewGoogleTranslator(),
		"deepl":        translator.NewDeepLTranslator(),
		"go-translate": goTranslateGoogle,
		"microsoft":    goTranslateMicrosoft,
	})
}

func newGoTranslateBackend(provider gtranslate.Provider) (translator.Translator, error) {
	client, err := gtranslate.NewTranslator(&gtranslate.TranslateOptions{
		Provider:      provider,
		HTTPClient:    &http.Client{Timeout: 20 * time.Second},
		GoogleAPIType: gtranslate.TypeClientGtx,
	})
	if err != nil {
		return nil, err
	}
	return goTranslateBackend{client: client}, nil
}

type goTranslateBackend struct {
	client gtranslate.Translator
}

func (b goTranslateBackend) Translate(ctx context.Context, text, from, to string) (*translator.Translated, error) {
	var detectedSource []string
	if from != "" && from != "auto" {
		detectedSource = []string{from}
	}
	translated, err := b.client.TranslateText(ctx, []string{text}, to, detectedSource...)
	if err != nil {
		return nil, err
	}
	if len(translated) == 0 || translated[0] == "" {
		return nil, fmt.Errorf("go-translate returned an empty result")
	}
	if from == "auto" {
		from = ""
	}
	return &translator.Translated{
		Text: translated[0],
		From: translator.TranslateFrom{
			Language: translator.TranslateFromLanguage{Iso: from},
		},
	}, nil
}

func newService(bundle *i18n.Bundle, backends map[string]translator.Translator) *Service {
	s := &Service{bundle: bundle, backends: backends}
	s.app = fiber.New(fiber.Config{BodyLimit: maxBodySize})
	s.app.Get("/hello/:name", s.helloHandler)
	s.app.Post("/translate", s.translateHandler)
	s.fiberHTTP = adaptor.FiberApp(s.app)
	return s
}

func loadBundle() (*i18n.Bundle, error) {
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	for _, path := range []string{"locales/active.en.toml", "locales/active.ar.toml"} {
		if _, err := bundle.LoadMessageFileFS(localeFiles, path); err != nil {
			return nil, err
		}
	}
	return bundle, nil
}

func (s *Service) Name() string { return "translation" }

// Routes يعرّف المسارات بصيغة ServeMux، ويُمرر الطلبات إلى تطبيق Fiber.
func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: http.MethodGet, Pattern: "/hello/", Handler: s.mountFiber},
		{Method: http.MethodPost, Pattern: "/translate", Handler: s.mountFiber},
	}
}

// mountFiber يحد حجم الجسم قبل أن يقرأه محوّل Fiber، ويمنع استهلاك ذاكرة غير محدود.
func (s *Service) mountFiber(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
		if err != nil {
			http.Error(w, `{"error":"request body too large or unreadable"}`, http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	s.fiberHTTP(w, r)
}

func (s *Service) helloHandler(c *fiber.Ctx) error {
	name := strings.TrimSpace(c.Params("name"))
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	localizer := i18n.NewLocalizer(s.bundle, c.Get("Accept-Language"))
	message, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID:    "HelloPerson",
		TemplateData: map[string]string{"Name": name},
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not localize greeting"})
	}
	return c.JSON(fiber.Map{"message": message})
}

type translateRequest struct {
	Text     string `json:"text"`
	To       string `json:"to"`
	From     string `json:"from,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type translateResponse struct {
	Text     string `json:"text"`
	To       string `json:"to"`
	Provider string `json:"provider"`
	From     string `json:"from,omitempty"`
}

func (s *Service) translateHandler(c *fiber.Ctx) error {
	var req translateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	req.Text = strings.TrimSpace(req.Text)
	req.To = strings.TrimSpace(req.To)
	req.From = strings.ToLower(strings.TrimSpace(req.From))
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	if req.Text == "" || req.To == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "text and to are required"})
	}
	targetLanguage, err := language.Parse(req.To)
	if err != nil || targetLanguage == language.Und {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to must be a valid language code, such as ar, en, fr, or zh-CN"})
	}
	req.To = targetLanguage.String()
	if req.From != "" && req.From != "auto" && req.From != "ar" && req.From != "en" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "from must be auto, ar, or en"})
	}
	if req.Provider == "" {
		req.Provider = "google"
	}
	backend, ok := s.backends[req.Provider]
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provider must be google, deepl, go-translate, or microsoft"})
	}
	if req.Provider == "microsoft" && (req.From == "" || req.From == "auto") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "from is required for the microsoft provider"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var result *translator.Translated
	if req.Provider == "go-translate" || req.Provider == "microsoft" {
		result, err = backend.Translate(ctx, req.Text, req.From, req.To)
	} else {
		result, err = translator.TranslateWith(ctx, backend, req.Text, req.To)
	}
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "translation provider failed"})
	}
	if result == nil || result.Text == "" {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "translation provider returned an empty result"})
	}
	return c.JSON(translateResponse{
		Text:     result.Text,
		To:       req.To,
		Provider: req.Provider,
		From:     result.From.Language.Iso,
	})
}

// decodeResponse متاح للاختبارات الداخلية عند فحص عقد الاستجابة JSON.
func decodeResponse(data []byte, target any) error { return json.Unmarshal(data, target) }
