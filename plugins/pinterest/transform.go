package pinterest

// transform.go — تحويل البيانات الخام إلى نتائج صور.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// rawPin يقابل شكل عنصر واحد كما يُستخرج من DOM عبر extractPinsJS —
// نفس حقول EXTRACT_JS بالضبط في pinterest_scraper.js السابق.
type rawPin struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	ImageSrc    string `json:"image_src"`
	ImageSrcset string `json:"image_srcset"`
}

// ─── تحويل rawPin -> imageResult (منطق pinToImage/originalFromSrcset/convertQuality) ─

var pinimgThumbRe = regexp.MustCompile(`https://i\.pinimg\.com/(?:\d+x|originals|736x|474x|236x)/`)

// pinimgSizeRe: مقاطع الحجم القابلة للاستبدال في convertQuality (ثابت — يُجمَّع مرة واحدة).
var pinimgSizeRe = regexp.MustCompile(`https://i\.pinimg\.com/(?:originals|736x|474x|236x)/`)
var pinimgOriginalInSrcsetRe = regexp.MustCompile(`(https://i\.pinimg\.com/originals/[^ \s]+\.(?:jpg|jpeg|png|webp))`)

// originalFromSrcset يقابل originalFromSrcset في النسخة السابقة: يفضّل
// رابط originals/ الصريح لو وُجد داخل srcset، وإلا يختار أعلى دقة
// (بالمعامل Nx) من عناصر srcset.
func originalFromSrcset(srcset string) string {
	if srcset == "" {
		return ""
	}
	if m := pinimgOriginalInSrcsetRe.FindStringSubmatch(srcset); m != nil {
		return m[1]
	}
	type entry struct {
		scale float64
		url   string
	}
	var entries []entry
	for chunk := range strings.SplitSeq(srcset, ",") {
		parts := strings.Fields(strings.TrimSpace(chunk))
		if len(parts) == 0 || !strings.HasPrefix(parts[0], "http") {
			continue
		}
		scale := 1.0
		if len(parts) > 1 {
			s := strings.TrimSuffix(parts[1], "x")
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				scale = f
			}
		}
		entries = append(entries, entry{scale, parts[0]})
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].scale > entries[j].scale })
	return entries[0].url
}

// convertQuality يقابل convertQuality في النسخة السابقة.
func convertQuality(u, quality string) string {
	if u == "" || quality == "original" {
		return u
	}
	return pinimgSizeRe.ReplaceAllString(u, "https://i.pinimg.com/"+quality+"/")
}

// pinFromRaw يقابل pinToImage في النسخة السابقة.
func pinFromRaw(p rawPin, quality string) *imageResult {
	fromSrcset := originalFromSrcset(p.ImageSrcset)
	imgURL := fromSrcset
	if imgURL == "" {
		imgURL = p.ImageSrc
	}
	if imgURL == "" {
		return nil
	}
	if fromSrcset == "" && p.ImageSrc != "" {
		imgURL = pinimgThumbRe.ReplaceAllString(p.ImageSrc, "https://i.pinimg.com/originals/")
	}

	return &imageResult{
		ID:        p.ID,
		URL:       convertQuality(imgURL, quality),
		Title:     p.Title,
		SourceURL: p.URL,
		Thumbnail: p.ImageSrc,
	}
}
