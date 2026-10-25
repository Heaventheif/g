package novel

// slug.go — توليد slug للروايات.

import (
	"regexp"
	"strings"
)

// ─── slugify ─────────────────────────────────────────────────────

var (
	nonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)
	trimDashRe = regexp.MustCompile(`^-|-$`)
)

func slugify(name string) string {
	lower := strings.ToLower(strings.ReplaceAll(name, "'", ""))
	dashed := nonAlnumRe.ReplaceAllString(lower, "-")
	return trimDashRe.ReplaceAllString(dashed, "")
}
