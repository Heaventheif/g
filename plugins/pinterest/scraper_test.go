package pinterest

import (
	"context"
	"errors"
	"testing"
	"time"

	"sunkenbot/internal/browser"
)

func TestWaitForChallengeToClearWithProbe_MatchesTitleAndBody(t *testing.T) {
	calls := 0
	err := waitForChallengeToClearWithProbe(context.Background(), time.Second, []string{"verify you are human"}, func(context.Context) (browser.ChallengeObservation, error) {
		calls++
		if calls == 1 {
			return browser.ChallengeObservation{Text: "title ordinary; BODY: VERIFY YOU ARE HUMAN"}, nil
		}
		return browser.ChallengeObservation{Text: "Pinterest"}, nil
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("probe calls = %d, want 2", calls)
	}
}

func TestWaitForChallengeToClearWithProbe_ProbeErrorProceeds(t *testing.T) {
	probeErr := errors.New("navigation changed during probe")
	calls := 0
	err := waitForChallengeToClearWithProbe(context.Background(), time.Second, pinterestChallengeMarkers, func(context.Context) (browser.ChallengeObservation, error) {
		calls++
		return browser.ChallengeObservation{}, probeErr
	})
	if err != nil {
		t.Fatalf("probe error should be treated as clear: %v", err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want immediate proceed", calls)
	}
}

func TestWaitForChallengeToClearWithProbe_TimeoutRemainsNonFatalToCaller(t *testing.T) {
	waitErr := waitForChallengeToClearWithProbe(context.Background(), 10*time.Millisecond, pinterestChallengeMarkers, func(context.Context) (browser.ChallengeObservation, error) {
		return browser.ChallengeObservation{Text: "Just a moment"}, nil
	})
	// The production wrapper intentionally discards this timeout, matching the
	// scraper's best-effort behavior.
	if !errors.Is(waitErr, browser.ErrChallengeTimeout) {
		t.Fatalf("wait error = %v, want timeout for caller to ignore", waitErr)
	}
}
