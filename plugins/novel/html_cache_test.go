package novel

import "testing"

func TestExtractBalancedTag_CachedAndUncachedTagsAgree(t *testing.T) {
	doc := `<div class="a"><div>inner</div>tail</div><custom>x</custom>`
	got, ok := extractBalancedTag(doc, "div", nil) // وسم مخزَّن مسبقاً
	if !ok || got != `<div>inner</div>tail` {
		t.Fatalf("div: %q %v", got, ok)
	}
	got, ok = extractBalancedTag(doc, "custom", nil) // وسم خارج الجدول ⇒ تجميع عند الطلب
	if !ok || got != "x" {
		t.Fatalf("custom: %q %v", got, ok)
	}
}

func TestNovelFullAttribute(t *testing.T) {
	tag := `<a href="/n/chapter-1.html" title='Chapter 1 &amp; more' data-x=7>`
	if got := novelFullAttribute(tag, "href"); got != "/n/chapter-1.html" {
		t.Errorf("href = %q", got)
	}
	if got := novelFullAttribute(tag, "title"); got != "Chapter 1 & more" {
		t.Errorf("title = %q", got)
	}
	if got := novelFullAttribute(tag, "data-x"); got != "7" { // خارج الجدول
		t.Errorf("data-x = %q", got)
	}
	if got := novelFullAttribute(tag, "missing"); got != "" {
		t.Errorf("missing = %q", got)
	}
}
