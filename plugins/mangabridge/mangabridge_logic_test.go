package mangabridge

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"One Piece", "one-piece"},
		{"  Attack On Titan  ", "attack-on-titan"},
		{"already-slugged", "already-slugged"},
		{"MULTIPLE   spaces", "multiple-spaces"},
		{"../One Piece?", "one-piece"},
		{"L'été", "lete"},
		{"", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidChapterNumber(t *testing.T) {
	for _, chapter := range []string{"1", "12.5", "100.25"} {
		if !validChapterNumber(chapter) {
			t.Errorf("validChapterNumber(%q) = false, want true", chapter)
		}
	}
	for _, chapter := range []string{"", "0", "-1", "1/2", "../1", "1.2.3", "12345678901234567"} {
		if validChapterNumber(chapter) {
			t.Errorf("validChapterNumber(%q) = true, want false", chapter)
		}
	}
}
