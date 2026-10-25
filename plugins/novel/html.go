package novel

// html.go — أدوات تحليل HTML الخفيفة.

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

// ─── محلّل HTML خفيف (بدون تبعيات) ───────────────────────────────

var tagStripRe = regexp.MustCompile(`(?is)<[^>]*>`)

// textOf يقابل .get_text() في بايثون على جزء HTML صغير: يزيل الوسوم
// الداخلية ويفك ترميز HTML entities.
func textOf(fragment string) string {
	return html.UnescapeString(tagStripRe.ReplaceAllString(fragment, ""))
}

type tagEvent struct {
	pos, end int
	open     bool
}

// extractBalancedTag يبحث عن أول وسم <tag> يحقق matchAttrs، ويُرجع
// محتواه الداخلي مع موازنة الوسوم المتداخلة من نفس النوع (تقابل
// soup.select_one(sel) من ناحية اختيار أول عنصر مطابق).
func extractBalancedTag(htmlStr, tag string, matchAttrs func(string) bool) (string, bool) {
	openRe := regexp.MustCompile(`(?is)<` + tag + `(?:\s[^>]*)?>`)
	closeRe := regexp.MustCompile(`(?is)</` + tag + `\s*>`)

	var events []tagEvent
	for _, o := range openRe.FindAllStringIndex(htmlStr, -1) {
		events = append(events, tagEvent{o[0], o[1], true})
	}
	for _, c := range closeRe.FindAllStringIndex(htmlStr, -1) {
		events = append(events, tagEvent{c[0], c[1], false})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })

	for i, e := range events {
		if !e.open {
			continue
		}
		openTagText := htmlStr[e.pos:e.end]
		if matchAttrs != nil && !matchAttrs(openTagText) {
			continue
		}
		depth := 1
		for j := i + 1; j < len(events); j++ {
			if events[j].open {
				depth++
			} else {
				depth--
				if depth == 0 {
					return htmlStr[e.end:events[j].pos], true
				}
			}
		}
	}
	return "", false
}

func attrEquals(attrName, value string) func(string) bool {
	re := regexp.MustCompile(`(?i)\b` + attrName + `\s*=\s*["']` + regexp.QuoteMeta(value) + `["']`)
	return func(openTag string) bool { return re.MatchString(openTag) }
}

func classToken(token string) func(string) bool {
	re := regexp.MustCompile(`(?i)\bclass\s*=\s*["']([^"']*)["']`)
	return func(openTag string) bool {
		m := re.FindStringSubmatch(openTag)
		if m == nil {
			return false
		}
		for c := range strings.FieldsSeq(m[1]) {
			if strings.EqualFold(c, token) {
				return true
			}
		}
		return false
	}
}

func classContainsSubstr(sub string) func(string) bool {
	re := regexp.MustCompile(`(?i)\bclass\s*=\s*["']([^"']*)["']`)
	return func(openTag string) bool {
		m := re.FindStringSubmatch(openTag)
		return m != nil && strings.Contains(strings.ToLower(m[1]), strings.ToLower(sub))
	}
}

// containerCandidateTags: عناصر HTML المرشّحة للاحتواء (تغطي الأغلبية
// الساحقة من مواقع روايات الويب الحقيقية).
var containerCandidateTags = []string{"div", "section", "article", "main"}

// contentSelector يقابل سطراً واحداً من selectors في SITE ببايثون.
type contentSelector struct {
	tag       string // "" = جرّب كل containerCandidateTags
	matchAttr func(string) bool
}

func findContainer(htmlStr string, selectors []contentSelector) (string, bool) {
	for _, sel := range selectors {
		if sel.tag != "" {
			if content, ok := extractBalancedTag(htmlStr, sel.tag, sel.matchAttr); ok && len(textOf(content)) > 200 {
				return content, true
			}
			continue
		}
		for _, tag := range containerCandidateTags {
			if content, ok := extractBalancedTag(htmlStr, tag, sel.matchAttr); ok && len(textOf(content)) > 200 {
				return content, true
			}
		}
	}
	return "", false
}
