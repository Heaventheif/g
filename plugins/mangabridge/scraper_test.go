package mangabridge

import (
	"context"
	"testing"
	"time"

	"sunkenbot/internal/browser"
)

func TestWaitForCloudflareTitleWithProbe_IsCaseSensitive(t *testing.T) {
	calls := 0
	err := waitForCloudflareTitleWithProbe(context.Background(), time.Second, func(context.Context) (browser.ChallengeObservation, error) {
		calls++
		return browser.ChallengeObservation{Text: "JUST A MOMENT"}, nil
	})
	if err != nil {
		t.Fatalf("different-case title should be considered clear: %v", err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
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
