package wait

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Every test runs inside a synctest bubble so the production-style timeout
// and interval are exercised on a fake clock instead of shortened durations.
const (
	testTimeout      = 30 * time.Second
	testPollInterval = 3 * time.Second
)

func testWaiter[T any]() *Waiter[T] {
	return New[T](testPollInterval, testTimeout)
}

func TestWaitForReturnsTheSuccessfulValue(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		start := time.Now()
		value, err := testWaiter[int]().WaitFor(context.Background(), func(context.Context) (int, error) {
			calls++
			if calls < 2 {
				return 0, fmt.Errorf("value not settled: %w", ErrNotReady)
			}
			return calls * 10, nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("expected two polls, calls=%d error=%v", calls, err)
		}
		if value != 20 {
			t.Fatalf("expected the settled value, got %d", value)
		}
		if waited := time.Since(start); waited != testPollInterval {
			t.Fatalf("expected one interval between polls, waited %s", waited)
		}
	})
}

// A poll slower than the interval must still be followed by a full interval
// of rest so the provider does not hammer an already slow backend.
func TestWaitForSpacesPollsFromTheEndOfEachCall(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const pollDuration = 5 * time.Second

		start := time.Now()
		var started []time.Duration
		_, err := testWaiter[int]().WaitFor(context.Background(), func(context.Context) (int, error) {
			started = append(started, time.Since(start))
			time.Sleep(pollDuration)
			if len(started) < 3 {
				return 0, ErrNotReady
			}
			return len(started), nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []time.Duration{0, pollDuration + testPollInterval, 2 * (pollDuration + testPollInterval)}
		if len(started) != len(want) {
			t.Fatalf("expected %d polls, got %d at %v", len(want), len(started), started)
		}
		for i, at := range want {
			if started[i] != at {
				t.Fatalf("poll %d started at %s, want %s (all: %v)", i+1, started[i], at, started)
			}
		}
	})
}

func TestWaitForTimesOutWithTheLastNotReadyReason(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		start := time.Now()
		_, err := testWaiter[int]().WaitFor(context.Background(), func(context.Context) (int, error) {
			calls++
			return 0, fmt.Errorf("attempt %d still provisioning: %w", calls, ErrNotReady)
		})
		if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrNotReady) {
			t.Fatalf("expected timeout and not-ready markers, got %v", err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("attempt %d still provisioning", calls)) {
			t.Fatalf("expected the last not-ready reason, got %q", err)
		}
		if waited := time.Since(start); waited != testTimeout {
			t.Fatalf("expected timeout after %s, waited %s", testTimeout, waited)
		}
	})
}

// Cancellation must win over the waiter's timeout, whether the poll function
// honors the context directly or reaches its own not-ready result.
func TestWaitForReportsCallerCancellation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		poll func(context.Context) (int, error)
	}{
		{
			name: "poll honors context",
			poll: func(ctx context.Context) (int, error) { return 0, ctx.Err() },
		},
		{
			name: "poll reports not ready",
			poll: func(context.Context) (int, error) { return 0, ErrNotReady },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				_, err := testWaiter[int]().WaitFor(ctx, test.poll)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
				if errors.Is(err, ErrTimeout) {
					t.Fatalf("caller cancellation must not be reported as timeout, got %v", err)
				}
			})
		})
	}
}

func TestWaitForReportsTimeoutReachedInsidePoll(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		_, err := testWaiter[int]().WaitFor(context.Background(), func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		})
		if !errors.Is(err, ErrTimeout) || errors.Is(err, context.Canceled) {
			t.Fatalf("expected waiter timeout, got %v", err)
		}
		if waited := time.Since(start); waited != testTimeout {
			t.Fatalf("expected timeout after %s, waited %s", testTimeout, waited)
		}
	})
}

// An operation deadline, such as one from a timeouts attribute, must read as a
// timeout with the last not-ready reason, not as a cancellation. A cause on the
// deadline is the error text, so that it shows the configured value instead of
// the time that was left for this wait.
func TestWaitForReportsContextDeadlineAsTimeout(t *testing.T) {
	t.Parallel()

	const deadline = 10 * time.Second
	for _, test := range []struct {
		name    string
		withCtx func(context.Context) (context.Context, context.CancelFunc)
		want    string
	}{
		{
			name: "deadline without cause",
			withCtx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(ctx, deadline)
			},
			want: "timeout: context deadline exceeded: status=pending: not ready",
		},
		{
			name: "deadline with timeout cause",
			withCtx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeoutCause(ctx, deadline, fmt.Errorf("%w after %s", ErrTimeout, deadline))
			},
			want: "timeout after 10s: status=pending: not ready",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := test.withCtx(context.Background())
				defer cancel()
				start := time.Now()
				// The waiter gets the same value as the deadline, as an operation
				// passes it to each waiter. The deadline of ctx must still win.
				w := New[int](testPollInterval, deadline)
				_, err := w.WaitFor(ctx, func(context.Context) (int, error) {
					return 0, fmt.Errorf("status=pending: %w", ErrNotReady)
				})
				if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrNotReady) || errors.Is(err, context.Canceled) {
					t.Fatalf("expected timeout with the not-ready reason, got %v", err)
				}
				if err.Error() != test.want {
					t.Fatalf("error = %q, want %q", err, test.want)
				}
				if waited := time.Since(start); waited != deadline {
					t.Fatalf("expected timeout after %s, waited %s", deadline, waited)
				}
			})
		})
	}
}

// A caller that cancels a context with a deadline cancels the wait. It is not a
// timeout.
func TestWaitForReportsCancellationBeforeDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeoutCause(context.Background(), time.Hour, fmt.Errorf("%w after 1h0m0s", ErrTimeout))
		defer cancel()
		time.AfterFunc(3*testPollInterval, cancel)
		_, err := testWaiter[int]().WaitFor(ctx, func(context.Context) (int, error) {
			return 0, ErrNotReady
		})
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	})
}

func TestWaitForReturnsFatalErrorAsIs(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		backendErr := errors.New("backend unavailable")
		start := time.Now()
		_, err := testWaiter[int]().WaitFor(context.Background(), func(context.Context) (int, error) {
			return 0, backendErr
		})
		if err != backendErr {
			t.Fatalf("expected the original fatal error, got %v", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("expected failure on the first poll, waited %s", waited)
		}
	})
}

// A caller can classify a terminal state as a fatal error instead of waiting
// for a timeout.
func TestWaitForStopsOnTerminalState(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		terminalErr := errors.New("entered error state")
		calls := 0
		_, err := testWaiter[string]().WaitFor(context.Background(), func(context.Context) (string, error) {
			calls++
			return "error", terminalErr
		})
		if err != terminalErr || calls != 1 {
			t.Fatalf("expected immediate terminal failure, calls=%d error=%v", calls, err)
		}
	})
}

// A delete poll can classify not-found as success by returning a nil error.
func TestWaitForLetsCallerTreatNotFoundAsSuccess(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		errNotFound := errors.New("not found")
		calls := 0
		get := func(context.Context) (int, error) {
			calls++
			if calls < 3 {
				return calls, nil
			}
			return 0, errNotFound
		}

		start := time.Now()
		_, err := testWaiter[int]().WaitFor(context.Background(), func(ctx context.Context) (int, error) {
			value, err := get(ctx)
			if errors.Is(err, errNotFound) {
				return 0, nil
			}
			if err != nil {
				return 0, err
			}
			return value, ErrNotReady
		})
		if err != nil || calls != 3 {
			t.Fatalf("expected success when the object disappeared, calls=%d error=%v", calls, err)
		}
		if waited := time.Since(start); waited != 2*testPollInterval {
			t.Fatalf("expected two intervals before success, waited %s", waited)
		}
	})
}

func TestNewAppliesDefaults(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		interval time.Duration
		timeout  time.Duration
	}{
		{name: "zero values"},
		{name: "negative values", interval: -time.Second, timeout: -time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			waiter := New[int](test.interval, test.timeout)
			if waiter.interval != defaultInterval || waiter.timeout != defaultTimeout {
				t.Fatalf("expected defaults %s/%s, got %s/%s", defaultInterval, defaultTimeout, waiter.interval, waiter.timeout)
			}
		})
	}
}

func TestNewKeepsPositiveDurations(t *testing.T) {
	t.Parallel()

	waiter := New[int](testPollInterval, testTimeout)
	if waiter.interval != testPollInterval || waiter.timeout != testTimeout {
		t.Fatalf("expected %s/%s, got %s/%s", testPollInterval, testTimeout, waiter.interval, waiter.timeout)
	}
}

func noJitter(level time.Duration) time.Duration {
	return level
}

func TestNewBackoffWithJitterGrowsTheRestAndHoldsTheLastLevel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		waiter := NewBackoffWithJitter[int](
			[]time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second},
			10*time.Minute,
		)
		waiter.jitter = noJitter

		start := time.Now()
		var started []time.Duration
		_, err := waiter.WaitFor(context.Background(), func(context.Context) (int, error) {
			started = append(started, time.Since(start))
			if len(started) < 6 {
				return 0, fmt.Errorf("busy: %w", ErrNotReady)
			}
			return 1, nil
		})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		want := []time.Duration{0, 10 * time.Second, 40 * time.Second, 100 * time.Second, 160 * time.Second, 220 * time.Second}
		if fmt.Sprint(started) != fmt.Sprint(want) {
			t.Fatalf("expected polls at %v, got %v", want, started)
		}
	})
}

func TestNewBackoffWithJitterTimesOutWithTheLastNotReadyReason(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const timeout = time.Minute
		waiter := NewBackoffWithJitter[int]([]time.Duration{10 * time.Second, 30 * time.Second}, timeout)
		waiter.jitter = noJitter

		start := time.Now()
		calls := 0
		_, err := waiter.WaitFor(context.Background(), func(context.Context) (int, error) {
			calls++
			return 0, fmt.Errorf("busy attempt %d: %w", calls, ErrNotReady)
		})
		if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "busy attempt 3") {
			t.Fatalf("expected a timeout with the last reason, got %v", err)
		}
		if waited := time.Since(start); waited != timeout {
			t.Fatalf("expected to give up after %s, waited %s", timeout, waited)
		}
		if calls != 3 {
			t.Fatalf("expected polls at 0s, 10s, and 40s, got %d polls", calls)
		}
	})
}

func TestNewBackoffWithJitterRestsBetweenHalfAndAllOfTheLevel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const level = 10 * time.Second
		waiter := NewBackoffWithJitter[int]([]time.Duration{level}, time.Minute)

		start := time.Now()
		calls := 0
		_, err := waiter.WaitFor(context.Background(), func(context.Context) (int, error) {
			calls++
			if calls < 2 {
				return 0, fmt.Errorf("busy: %w", ErrNotReady)
			}
			return 1, nil
		})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		if waited := time.Since(start); waited < level/2 || waited > level {
			t.Fatalf("expected a rest between %s and %s, waited %s", level/2, level, waited)
		}
	})
}

func TestEqualJitterStaysWithinHalfAndAllOfTheLevel(t *testing.T) {
	t.Parallel()

	for _, level := range []time.Duration{time.Nanosecond, 3 * time.Nanosecond, 10 * time.Second} {
		for range 1000 {
			if got := equalJitter(level); got < level/2 || got > level {
				t.Fatalf("level %s: expected a value between %s and %s, got %s", level, level/2, level, got)
			}
		}
	}
}

func TestNewBackoffWithJitterAppliesDefaults(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		delays  []time.Duration
		timeout time.Duration
		want    []time.Duration
	}{
		{name: "empty schedule", want: []time.Duration{defaultInterval}},
		{
			name:    "non-positive values",
			delays:  []time.Duration{0, 30 * time.Second, -time.Second},
			timeout: -time.Second,
			want:    []time.Duration{defaultInterval, 30 * time.Second, defaultInterval},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			waiter := NewBackoffWithJitter[int](test.delays, test.timeout)
			if fmt.Sprint(waiter.delays) != fmt.Sprint(test.want) || waiter.timeout != defaultTimeout {
				t.Fatalf("expected %v/%s, got %v/%s", test.want, defaultTimeout, waiter.delays, waiter.timeout)
			}
		})
	}
}

func TestNewBackoffWithJitterCopiesTheSchedule(t *testing.T) {
	t.Parallel()

	delays := []time.Duration{10 * time.Second, 30 * time.Second}
	waiter := NewBackoffWithJitter[int](delays, time.Minute)
	delays[0] = time.Hour
	if waiter.delays[0] != 10*time.Second {
		t.Fatalf("expected the waiter to keep its own schedule, got %v", waiter.delays)
	}
}
