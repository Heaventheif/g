package browser

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ErrChallengeTimeout indicates that a challenge remained active until the
// configured timeout elapsed.
var ErrChallengeTimeout = errors.New("challenge did not clear before timeout")

// ChallengeObservation is one snapshot of a page. Active can represent
// non-text signals such as challenge DOM elements; Text is checked for markers.
type ChallengeObservation struct {
	Active bool
	Text   string
}

// ChallengeProbe inspects the current page. Implementations should honor ctx.
type ChallengeProbe func(ctx context.Context) (ChallengeObservation, error)

// ProbeErrorPolicy controls what a wait does when a page probe fails.
type ProbeErrorPolicy uint8

const (
	// ProbeErrorRetry treats the page as not ready and tries again until timeout.
	ProbeErrorRetry ProbeErrorPolicy = iota
	// ProbeErrorClear preserves callers that proceed when inspection is unreliable.
	ProbeErrorClear
	// ProbeErrorReturn returns the first probe error to the caller.
	ProbeErrorReturn
)

// ChallengeWaitOptions configures a shared challenge wait. A supplied Probe
// makes the wait deterministic in tests and permits site-specific observations.
type ChallengeWaitOptions struct {
	Markers       []string
	Timeout       time.Duration
	CheckTimeout  time.Duration
	PollInterval  time.Duration
	NextInterval  func() time.Duration
	CaseSensitive bool
	ProbeError    ProbeErrorPolicy
	FinalCheck    bool
	TimeoutError  error
	Probe         ChallengeProbe
}

// WaitForChallengeClear waits until none of markers appear in the page title
// and body text, or until timeout. It uses case-insensitive matching and a
// browser-backed title/body probe; specialized users can use
// WaitForChallengeClearWithOptions to retain distinct probing semantics.
func WaitForChallengeClear(ctx context.Context, markers []string, timeout time.Duration) error {
	return WaitForChallengeClearWithOptions(ctx, ChallengeWaitOptions{
		Markers: markers, Timeout: timeout,
		CheckTimeout: 3 * time.Second, PollInterval: 500 * time.Millisecond,
		ProbeError: ProbeErrorRetry, Probe: pageTextProbe,
	})
}

// WaitForChallengeClearWithOptions is the configurable form of
// WaitForChallengeClear. A zero CheckTimeout defaults to 3 seconds; a zero
// PollInterval defaults to 1 second. Timeout is the total wait budget.
func WaitForChallengeClearWithOptions(parent context.Context, opts ChallengeWaitOptions) error {
	if opts.CheckTimeout <= 0 {
		opts.CheckTimeout = 3 * time.Second
	}
	if opts.PollInterval <= 0 && opts.NextInterval == nil {
		opts.PollInterval = time.Second
	}
	if opts.TimeoutError == nil {
		opts.TimeoutError = ErrChallengeTimeout
	}
	if opts.Probe == nil {
		opts.Probe = pageTextProbe
	}

	waitCtx, cancel := context.WithTimeout(parent, opts.Timeout)
	defer cancel()

	for {
		obs, err := runChallengeProbe(waitCtx, opts)
		if parent.Err() != nil {
			return parent.Err()
		}
		if err == nil {
			if !challengeObservationActive(obs, opts) {
				return nil
			}
		} else {
			if parent.Err() != nil {
				return parent.Err()
			}
			switch opts.ProbeError {
			case ProbeErrorClear:
				return nil
			case ProbeErrorReturn:
				return err
			}
		}

		if waitCtx.Err() != nil {
			if parent.Err() != nil {
				return parent.Err()
			}
			if opts.FinalCheck {
				finalObs, finalErr := runChallengeProbe(parent, opts)
				if finalErr == nil && !challengeObservationActive(finalObs, opts) {
					return nil
				}
				if finalErr != nil && opts.ProbeError == ProbeErrorReturn {
					return finalErr
				}
			}
			return opts.TimeoutError
		}

		delay := opts.PollInterval
		if opts.NextInterval != nil {
			delay = opts.NextInterval()
		}
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func runChallengeProbe(ctx context.Context, opts ChallengeWaitOptions) (ChallengeObservation, error) {
	checkCtx, cancel := context.WithTimeout(ctx, opts.CheckTimeout)
	defer cancel()
	return opts.Probe(checkCtx)
}

func challengeObservationActive(obs ChallengeObservation, opts ChallengeWaitOptions) bool {
	if obs.Active {
		return true
	}
	return HasChallengeMarker(obs.Text, opts.Markers, opts.CaseSensitive)
}

// HasChallengeMarker reports whether text contains any non-empty marker.
// Matching is case-insensitive unless caseSensitive is true.
func HasChallengeMarker(text string, markers []string, caseSensitive bool) bool {
	if !caseSensitive {
		text = strings.ToLower(text)
	}
	for _, marker := range markers {
		if marker == "" {
			continue
		}
		if !caseSensitive {
			marker = strings.ToLower(marker)
		}
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

type pageText struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func pageTextProbe(ctx context.Context) (ChallengeObservation, error) {
	var text pageText
	err := chromedp.Run(ctx, chromedp.Evaluate(`(() => ({title: document.title || '', body: document.body ? document.body.innerText : ''}))()`, &text))
	return ChallengeObservation{Text: text.Title + " " + text.Body}, err
}
