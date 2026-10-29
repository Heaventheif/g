package mangabridge

import (
	"context"
	"testing"
	"time"

	"sunkenbot/internal/browser"
)

// المطابقة غير حساسة لحالة الأحرف: أي كتابة لـ "just a moment" تبقي التحدي قائماً.
func TestWaitForCloudflareTitleWithProbe_IsCaseInsensitive(t *testing.T) {
	for _, title := range []string{"Just a moment...", "JUST A MOMENT", "just a moment"} {
		err := waitForCloudflareTitleWithProbe(context.Background(), 30*time.Millisecond, func(context.Context) (browser.ChallengeObservation, error) {
			return browser.ChallengeObservation{Text: title}, nil
		})
		if err == nil {
			t.Errorf("%q يجب أن يُعدّ تحدياً قائماً (مهلة متوقعة)", title)
		}
	}
	err := waitForCloudflareTitleWithProbe(context.Background(), time.Second, func(context.Context) (browser.ChallengeObservation, error) {
		return browser.ChallengeObservation{Text: "Chapter 12"}, nil
	})
	if err != nil {
		t.Fatalf("عنوان عادي يجب أن يُعدّ صافياً: %v", err)
	}
}

func TestWaitForCloudflareTitleWithProbe_FinalCheckCanClear(t *testing.T) {
	calls := 0
	err := waitForCloudflareTitleWithProbe(context.Background(), 15*time.Millisecond, func(context.Context) (browser.ChallengeObservation, error) {
		calls++
		if calls == 1 {
			return browser.ChallengeObservation{Text: "Just a moment"}, nil
		}
		return browser.ChallengeObservation{Text: "Chapter"}, nil
	})
	if err != nil {
		t.Fatalf("final title check should clear the challenge: %v", err)
	}
	if calls < 2 {
		t.Fatalf("probe calls = %d, expected initial and final checks", calls)
	}
}

func TestWaitForCloudflareTitleWithProbe_RetriesProbeError(t *testing.T) {
	calls := 0
	err := waitForCloudflareTitleWithProbe(context.Background(), time.Second, func(context.Context) (browser.ChallengeObservation, error) {
		calls++
		if calls == 1 {
			return browser.ChallengeObservation{}, context.DeadlineExceeded
		}
		return browser.ChallengeObservation{Text: "Ready"}, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("wait = %v after %d calls, want nil after retry", err, calls)
	}
}
