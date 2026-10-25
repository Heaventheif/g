package mangabridge

// validation.go — تنظيف وفحص مدخلات المستخدم.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	mangaSlugInvalidRe = regexp.MustCompile(`[^a-z0-9]+`)
	mangaChapterRe     = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	jobIDRe            = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

func slugify(name string) string {
	name = strings.ToLower(strings.NewReplacer("'", "", "’", "").Replace(strings.TrimSpace(name)))
	decomposed := norm.NFD.String(name)
	var asciiBase strings.Builder
	for _, r := range decomposed {
		if !unicode.Is(unicode.Mn, r) {
			asciiBase.WriteRune(r)
		}
	}
	name = asciiBase.String()
	return strings.Trim(mangaSlugInvalidRe.ReplaceAllString(name, "-"), "-")
}

func validChapterNumber(chapter string) bool {
	if len(chapter) == 0 || len(chapter) > 16 || !mangaChapterRe.MatchString(chapter) {
		return false
	}
	n, err := strconv.ParseFloat(chapter, 64)
	return err == nil && n > 0 && n <= 100000
}
