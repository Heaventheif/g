package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChallengeActive(t *testing.T) {
	o := CloudflareOptions{}.withDefaults()
	cases := []struct {
		name string
		p    probeResult
		o    CloudflareOptions
		want bool
	}{
		{"عنصر تحدي", probeResult{ElementPresent: true}, o, true},
		{"عنوان Just a moment", probeResult{Title: "Just a moment..."}, o, true},
		{"صفحة عادية", probeResult{Title: "One Piece 1100", Body: "just a moment in the story"}, o, false},
		{"نص الجسم مع marker", probeResult{Title: "x", Body: "Checking your browser"},
			CloudflareOptions{BodyMarkers: []string{"checking your browser"}}.withDefaults(), true},
		{"marker عنوان مخصص", probeResult{Title: "Attention Required | Cloudflare"},
			CloudflareOptions{TitleMarkers: []string{"attention required"}}.withDefaults(), true},
	}
	for _, c := range cases {
		if got := c.p.challengeActive(c.o); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// فحص عالق (يتجاهل كل شيء حتى يُلغى سياقه) يجب ألا يتجاوز Timeout الكلي —
// هذا ما كان يفشل مع WaitNotPresent بلا مهلة لكل استدعاء.
func TestAwait_HungProbeRespectsOverallTimeout(t *testing.T) {
	probe := func(ctx context.Context) (probeResult, error) {
		<-ctx.Done()
		return probeResult{}, ctx.Err()
	}
	opts := CloudflareOptions{Timeout: 400 * time.Millisecond, CheckTimeout: 100 * time.Millisecond}.withDefaults()
	start := time.Now()
	err := awaitWith(context.Background(), opts, probe)
	if err == nil {
		t.Fatal("متوقع خطأ مهلة")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("الانتظار %v تجاوز السقف بكثير", el)
	}
}

func TestAwait_ReturnsNilWhenChallengeClears(t *testing.T) {
	calls := 0
	probe := func(ctx context.Context) (probeResult, error) {
		calls++
		if calls < 3 {
			return probeResult{ElementPresent: true}, nil
		}
		return probeResult{Title: "ok"}, nil
	}
	opts := CloudflareOptions{Timeout: 5 * time.Second}.withDefaults()
	if err := awaitWith(context.Background(), opts, probe); err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestAwait_ParentCancelReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	probe := func(ctx context.Context) (probeResult, error) { return probeResult{ElementPresent: true}, nil }
	err := awaitWith(ctx, CloudflareOptions{Timeout: 10 * time.Second}.withDefaults(), probe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v، المتوقع context.Canceled", err)
	}
}
