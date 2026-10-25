package pinterest

// ferdev.go — مصدر البحث البديل (ferdev) وترميز المصغّرات.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"sunkenbot/internal/keyrotate"
	"sunkenbot/internal/strutil"
)

// ferdevKeys يدير مجموعة مفاتيح Ferdev (تدوير Round-Robin + تبديل تلقائي عند
// 429) من متغير بيئة واحد FERDEV_API_KEY — يقبل مفتاحاً واحداً أو عدة مفاتيح
// مفصولة بفواصل (مثل: key1,key2,key3).
var ferdevKeys = sync.OnceValue(func() *keyrotate.Manager {
	return keyrotate.FromEnv("FERDEV_API_KEY")
})

// ─── Ferdev fallback (نفس _ferdev_search في بايثون) ─────────────────

// ferdevSearch يدوّر مفاتيح Ferdev (Round-Robin يبدأ من مفتاح مختلف كل طلب،
// مع تبديل تلقائي للمفتاح التالي عند 429) عبر ferdevKeys().Do حتى ينجح أحد
// المفاتيح أو تُستنفد كلها.
func (s *Service) ferdevSearch(ctx context.Context, query string, limit int) []imageResult {
	mgr := ferdevKeys()
	if mgr.Empty() {
		return nil
	}

	var data map[string]any
	_ = mgr.Do(func(apiKey string) (bool, error) {
		u := fmt.Sprintf("https://api.ferdev.my.id/search/pinterest?query=%s&apikey=%s&limit=%d",
			url.QueryEscape(query), url.QueryEscape(apiKey), limit)

		ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctxTimeout, http.MethodGet, u, nil)
		if err != nil {
			return false, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return true, fmt.Errorf("ferdev rate limited (429)")
		}
		body, _ := io.ReadAll(resp.Body)

		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return false, err
		}
		data = parsed
		return false, nil
	})
	if data == nil {
		return nil
	}

	var rawResults []any
	for _, key := range []string{"result", "data", "results"} {
		if arr, ok := data[key].([]any); ok {
			rawResults = arr
			break
		}
	}

	var out []imageResult
	for i, item := range rawResults {
		if i >= limit {
			break
		}
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		imgURL := strutil.FirstNonEmpty(
			strutil.StringOr(obj["image"], ""),
			strutil.StringOr(obj["url"], ""),
			strutil.StringOr(obj["imageUrl"], ""),
		)
		if imgURL == "" {
			if imgs, ok := obj["images"].([]any); ok && len(imgs) > 0 {
				imgURL = strutil.StringOr(imgs[0], "")
			}
		}
		if imgURL == "" {
			continue
		}

		pinID := strutil.StringOr(obj["id"], "")
		if pinID == "" {
			if pin, ok := obj["pin"].(map[string]any); ok {
				pinID = strutil.StringOr(pin["id"], "")
			}
		}
		if pinID == "" {
			pinID = fmt.Sprintf("ferdev_%d", i)
		}

		out = append(out, imageResult{
			ID:        pinID,
			URL:       imgURL,
			Title:     strutil.FirstNonEmpty(strutil.StringOr(obj["title"], ""), strutil.StringOr(obj["description"], "")),
			SourceURL: strutil.FirstNonEmpty(strutil.StringOr(obj["source"], ""), strutil.StringOr(obj["link"], "")),
		})
	}
	return out
}

// ─── ترميز base64 مقصوص (نفس منطق بايثون بالضبط، حتى لو كان تقصيره غريباً) ─

// ملاحظة (متعمَّد): التقصير إلى 120 حرفاً من base64 ثم "..." يجعل Thumbnail
// «علامة مرئية» فقط — ليس data URI صالحاً كصورة قابلة للعرض. لا تُحوِّله إلى
// صورة كاملة دون تغيير عقد الاستجابة مع العميل.
func (s *Service) encodeThumbnailsBase64(ctx context.Context, images []imageResult) {
	var wg sync.WaitGroup
	for i := range images {
		wg.Add(1)
		go func(img *imageResult) {
			defer wg.Done()
			ctxTimeout, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctxTimeout, http.MethodGet, img.URL, nil)
			if err != nil {
				return
			}
			resp, err := s.client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			content, err := io.ReadAll(resp.Body)
			if err != nil {
				return
			}

			mime := "image/jpeg"
			low := strings.ToLower(img.URL)
			if strings.HasSuffix(low, ".png") {
				mime = "image/png"
			} else if strings.HasSuffix(low, ".webp") {
				mime = "image/webp"
			}

			b64 := base64.StdEncoding.EncodeToString(content)
			if len(b64) > 120 {
				b64 = b64[:120]
			}
			img.Thumbnail = fmt.Sprintf("data:%s;base64,%s...", mime, b64)
		}(&images[i])
	}
	wg.Wait()
}
