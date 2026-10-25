package novel

// extract.go — استخراج نص الفصل وعنوانه وتنظيفه.

import (
	"regexp"
	"strings"
)

// ─── فلترة النص (نفس FILTER_WORDS/STOLEN في بايثون) ────────────────

var filterWords = []string{
	"advertisement", "report chapter", "next chapter", "prev chapter",
	"table of contents", "access denied", "just a moment", "cloudflare",
	"enable javascript", "read more at",
	"cookie", "privacy", "terms of service", "subscribe",
}

var stolenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)stol(en|e)\s+(content|chapter)`),
	regexp.MustCompile(`(?i)if\s+you.re\s+reading\s+this\s+on`),
	regexp.MustCompile(`(?i)unauthorized\s+(use|reproduction)`),
}

func isFiltered(t string) bool {
	if len(t) < 15 {
		return true
	}
	lo := strings.ToLower(t)
	for _, w := range filterWords {
		if strings.Contains(lo, w) {
			return true
		}
	}
	for _, p := range stolenPatterns {
		if p.MatchString(t) {
			return true
		}
	}
	return false
}

var (
	multiSpaceRe = regexp.MustCompile(`\s{2,}`)
	multiDotRe   = regexp.MustCompile(`\.{4,}`)
)

func clean(t string) string {
	t = strings.ReplaceAll(t, "\u00a0", " ")
	t = multiDotRe.ReplaceAllString(t, "...")
	t = multiSpaceRe.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

var paragraphRe = regexp.MustCompile(`(?is)<p(?:\s[^>]*)?>(.*?)</p>`)

func extractParagraphsFrom(containerHTML string) []string {
	matches := paragraphRe.FindAllStringSubmatch(containerHTML, -1)
	var paras []string
	for _, m := range matches {
		t := clean(textOf(m[1]))
		if len(t) > 15 && !isFiltered(t) {
			paras = append(paras, t)
		}
	}

	if len(paras) < 3 {
		// جرب التقسيم على الأسطر (بعد استبدال <br> بأسطر جديدة)
		withBreaks := regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(containerHTML, "\n")
		lines := strings.Split(textOf(withBreaks), "\n")
		paras = paras[:0]
		for _, l := range lines {
			t := clean(l)
			if len(t) > 15 && !isFiltered(t) {
				paras = append(paras, t)
			}
		}
	}

	if len(paras) < 2 {
		text := textOf(containerHTML)
		sentences := regexp.MustCompile(`[.!?]\s+`).Split(text, -1)
		paras = paras[:0]
		for _, s := range sentences {
			t := clean(s)
			if len(t) > 20 && !isFiltered(t) {
				paras = append(paras, t)
			}
		}
	}

	if len(paras) >= 2 {
		return paras
	}
	return nil
}

// extract يقابل extract(html, selectors) في بايثون بالكامل.
func extract(htmlStr string, selectors []contentSelector) []string {
	// إزالة العناصر المزعجة أولاً (script/style/ins/.ads/noscript/nav/header/footer/.advertisement)
	cleaned := regexp.MustCompile(`(?is)<(script|style|noscript|nav|header|footer)\b[^>]*>.*?</(?:script|style|noscript|nav|header|footer)>`).
		ReplaceAllString(htmlStr, "")

	container, ok := findContainer(cleaned, selectors)
	if !ok {
		if c, ok2 := extractBalancedTag(cleaned, "div", classToken("m-read")); ok2 && len(textOf(c)) > 200 {
			container = c
		} else if body, ok3 := extractBalancedTag(cleaned, "body", nil); ok3 {
			container = body
		} else {
			return nil
		}
	}

	return extractParagraphsFrom(container)
}

var titleSplitRe = regexp.MustCompile(`[–\-|]`)

func extractTitle(htmlStr string) string {
	candidates := []contentSelector{
		{tag: "h1", matchAttr: classToken("tit")},
		{tag: "", matchAttr: classToken("tit")},
		{tag: "h1", matchAttr: nil},
	}
	for _, sel := range candidates {
		tags := []string{sel.tag}
		if sel.tag == "" {
			tags = []string{"h1", "div", "span"}
		}
		for _, tag := range tags {
			if content, ok := extractBalancedTag(htmlStr, tag, sel.matchAttr); ok {
				t := strings.TrimSpace(titleSplitRe.Split(textOf(content), 2)[0])
				if len(t) > 2 {
					return t
				}
			}
		}
	}

	if titleTag, ok := extractBalancedTag(htmlStr, "title", nil); ok {
		t := strings.TrimSpace(textOf(titleTag))
		if len(t) > 2 {
			return strings.TrimSpace(strings.SplitN(t, " - ", 2)[0])
		}
	}
	return ""
}
