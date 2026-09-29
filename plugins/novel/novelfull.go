package novel

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const novelFullBaseURL = "https://novelfull.com"

var (
	novelFullAnchorRe      = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a\s*>`)
	novelFullChapterRe     = regexp.MustCompile(`(?i)\bchapter\s*(\d+(?:\s*\.\s*\d+)?)`)
	novelFullChapterPathRe = regexp.MustCompile(`(?i)(?:^|/)chapter-[^/]+\.html$`)
)

func novelFullAttribute(openTag, name string) string {
	pattern := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(name) + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	match := pattern.FindStringSubmatch(openTag)
	if match == nil {
		return ""
	}
	for _, value := range match[1:] {
		if value != "" {
			return html.UnescapeString(value)
		}
	}
	return ""
}

func normalizedChapterNumber(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	if !chapterNumberPattern.MatchString(value) {
		return ""
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 || number > 100000 {
		return ""
	}
	return strconv.FormatFloat(number, 'f', -1, 64)
}

func novelFullIndexPages(chapter string) []int {
	number, err := strconv.ParseFloat(chapter, 64)
	if err != nil || number <= 0 || number > 100000 {
		return nil
	}
	center := int(number / 50)
	if number-float64(center*50) > 0 {
		center++
	}
	if center < 1 {
		center = 1
	}
	candidates := []int{center, center + 1, center - 1, center + 2, center - 2, 1}
	seen := make(map[int]bool, len(candidates))
	pages := make([]int, 0, len(candidates))
	for _, page := range candidates {
		if page > 0 && !seen[page] {
			seen[page] = true
			pages = append(pages, page)
		}
	}
	return pages
}

// findNovelFullChapterURL resolves a chapter from its visible chapter label,
// not from the numeric portion of the URL (NovelFull encodes chapter 1.1 as
// a URL beginning with chapter-11, which otherwise collides with chapter 11).
func findNovelFullChapterURL(indexHTML, requestedChapter string) string {
	target := normalizedChapterNumber(requestedChapter)
	if target == "" {
		return ""
	}
	base, _ := url.Parse(novelFullBaseURL + "/")
	for _, anchor := range novelFullAnchorRe.FindAllStringSubmatch(indexHTML, -1) {
		openTag, body := anchor[1], anchor[2]
		labels := textOf(body) + " " + novelFullAttribute(openTag, "title")
		matches := novelFullChapterRe.FindAllStringSubmatch(labels, -1)
		matched := false
		for _, chapter := range matches {
			if normalizedChapterNumber(chapter[1]) == target {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		href := novelFullAttribute(openTag, "href")
		if href == "" {
			continue
		}
		candidate, err := base.Parse(href)
		if err != nil || candidate.Scheme != "https" || candidate.User != nil {
			continue
		}
		host := strings.ToLower(candidate.Hostname())
		if host != "novelfull.com" && !strings.HasSuffix(host, ".novelfull.com") {
			continue
		}
		if !novelFullChapterPathRe.MatchString(candidate.Path) {
			continue
		}
		return candidate.String()
	}
	return ""
}

func (s *Service) fetchNovelFullChapter(ctx context.Context, slug, chapter string) (string, string, error) {
	if slug == "" {
		return "", "", fmt.Errorf("اسم الرواية غير صالح")
	}
	for _, page := range novelFullIndexPages(chapter) {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("per-page", "50")
		indexURL := fmt.Sprintf("%s/%s.html?%s", novelFullBaseURL, slug, query.Encode())
		indexHTML := s.fetchPageHTTP(ctx, indexURL)
		if indexHTML == "" {
			indexHTML = fetchPageBrowser(ctx, indexURL)
		}
		if indexHTML == "" {
			continue
		}
		chapterURL := findNovelFullChapterURL(indexHTML, chapter)
		if chapterURL == "" {
			continue
		}
		chapterHTML := s.fetchPageHTTP(ctx, chapterURL)
		if chapterHTML == "" {
			chapterHTML = fetchPageBrowser(ctx, chapterURL)
		}
		if chapterHTML != "" {
			return chapterHTML, chapterURL, nil
		}
		return "", chapterURL, fmt.Errorf("تعذّر جلب صفحة الفصل من NovelFull")
	}
	return "", "", fmt.Errorf("لم يُعثر على الفصل %s في فهرس NovelFull", chapter)
}
