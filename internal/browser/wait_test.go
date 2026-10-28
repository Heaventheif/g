package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHasChallengeMarkerMatching(t *testing.T) {
	if !HasChallengeMarker("The title says JUST A MOMENT", []string{"just a moment"}, false) {
		t.Fatal("case-insensitive marker was not detected")
	}
	if HasChallengeMarker("The title says JUST A MOMENT", []string{"Just a moment"}, true) {
		t.Fatal("case-sensitive matcher accepted a different case")
	}
	if HasChallengeMarker("ordinary content", []string{"", "missing"}, false) {
		t.Fatal("empty or absent marker was detected")
	}
}

func TestWaitForChallengeClearHonorsCanceledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForChallengeClear(ctx, []string{"challenge"}, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForChallengeClear error = %v, want context.Canceled", err)
	}
}

func TestWaitForChallengeClearWithOptions_ClearsOnMarkerRemoval(t *testing.T) {
	calls := 0
	err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
		Markers: []string{"challenge"}, Timeout: time.Second, PollInterval: time.Millisecond,
		Probe: func(context.Context) (ChallengeObservation, error) {
			calls++
			if calls == 1 {
				return ChallengeObservation{Text: "CHALLENGE active"}, nil
			}
			return ChallengeObservation{Text: "chapter loaded"}, nil
		},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("probe calls = %d, want 2", calls)
	}
}

func TestWaitForChallengeClearWithOptions_ProbeErrorPolicies(t *testing.T) {
	probeErr := errors.New("probe failed")
	t.Run("clear", func(t *testing.T) {
		err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
			Timeout: time.Second, ProbeError: ProbeErrorClear,
			Probe: func(context.Context) (ChallengeObservation, error) { return ChallengeObservation{}, probeErr },
		})
		if err != nil {
			t.Fatalf("error policy clear returned %v", err)
		}
	})
	t.Run("retry", func(t *testing.T) {
		calls := 0
		err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, ProbeError: ProbeErrorRetry,
			Probe: func(context.Context) (ChallengeObservation, error) {
				calls++
				if calls == 1 {
					return ChallengeObservation{}, probeErr
				}
				return ChallengeObservation{}, nil
			},
		})
		if err != nil || calls != 2 {
			t.Fatalf("wait = %v after %d calls, want nil after 2", err, calls)
		}
	})
	t.Run("return", func(t *testing.T) {
		err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
			Timeout: time.Second, ProbeError: ProbeErrorReturn,
			Probe: func(context.Context) (ChallengeObservation, error) { return ChallengeObservation{}, probeErr },
		})
		if !errors.Is(err, probeErr) {
			t.Fatalf("wait error = %v, want probe error", err)
		}
	})
}

func TestWaitForChallengeClearWithOptions_TimeoutAndFinalCheck(t *testing.T) {
	calls := 0
	err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
		Markers: []string{"Just a moment"}, CaseSensitive: true,
		Timeout: 20 * time.Millisecond, CheckTimeout: time.Second,
		PollInterval: 5 * time.Millisecond, FinalCheck: true,
		Probe: func(context.Context) (ChallengeObservation, error) {
			calls++
			if calls == 1 {
				return ChallengeObservation{Text: "Just a moment"}, nil
			}
			return ChallengeObservation{Text: "Ready"}, nil
		},
	})
	if err != nil {
		t.Fatalf("final probe should observe cleared page, got %v", err)
	}
	if calls < 2 {
		t.Fatalf("probe calls = %d, expected final probe", calls)
	}
}

func TestWaitForChallengeClearWithOptions_Timeout(t *testing.T) {
	err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
		Timeout: 20 * time.Millisecond, PollInterval: 2 * time.Millisecond,
		Probe: func(context.Context) (ChallengeObservation, error) {
			return ChallengeObservation{Active: true}, nil
		},
	})
	if !errors.Is(err, ErrChallengeTimeout) {
		t.Fatalf("wait error = %v, want ErrChallengeTimeout", err)
	}
}

func TestWaitForChallengeClearWithOptions_ParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	err := WaitForChallengeClearWithOptions(ctx, ChallengeWaitOptions{
		Timeout: time.Second,
		Probe: func(ctx context.Context) (ChallengeObservation, error) {
			<-ctx.Done()
			return ChallengeObservation{}, ctx.Err()
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context.Canceled", err)
	}
}

func TestWaitForChallengeClearWithOptions_CheckTimeout(t *testing.T) {
	start := time.Now()
	err := WaitForChallengeClearWithOptions(context.Background(), ChallengeWaitOptions{
		Timeout: 50 * time.Millisecond, CheckTimeout: 10 * time.Millisecond,
		PollInterval: time.Millisecond,
		Probe: func(ctx context.Context) (ChallengeObservation, error) {
			<-ctx.Done()
			return ChallengeObservation{}, ctx.Err()
		},
	})
	if !errors.Is(err, ErrChallengeTimeout) {
		t.Fatalf("wait error = %v, want timeout", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("hung probe exceeded overall timeout: %v", time.Since(start))
	}
}
