// Package wait repeatedly checks an asynchronous backend operation until it
// settles. Deadline derivation, cancellation, timeout attribution, poll spacing
// and not-ready reporting are identical for every resource, so they live here
// rather than in each one.
//
// The SDK owns HTTP retry. NewBackoffWithJitter only spaces out the replay of
// busy refusals.
package wait

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	defaultInterval = 5 * time.Second
	defaultTimeout  = 10 * time.Minute
)

// ErrNotReady tells the waiter the resource is not in its target state yet and
// polling should continue. Wrap it to explain why; the reason surfaces in the
// timeout error.
//
//	return nil, fmt.Errorf("status=%s: %w", status, waiter.ErrNotReady)
var ErrNotReady = errors.New("not ready")

// ErrTimeout marks a wait whose own timeout elapsed, or whose context reached
// its deadline. A cancelled context is not a timeout. A caller that sets the
// deadline with context.WithTimeoutCause and a cause that wraps ErrTimeout,
// such as "timeout after 1h0m0s", gets that cause as the error text.
var ErrTimeout = errors.New("timeout")

// Waiter repeatedly runs a poll function until it succeeds.
type Waiter[T any] struct {
	interval time.Duration
	timeout  time.Duration
	// delays and jitter are set only by NewBackoffWithJitter.
	delays []time.Duration
	jitter func(level time.Duration) time.Duration
}

// New returns a Waiter that polls every interval and gives up after
// timeout. Non-positive values fall back to five seconds and ten minutes.
func New[T any](interval, timeout time.Duration) *Waiter[T] {
	if interval <= 0 {
		interval = defaultInterval
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Waiter[T]{interval: interval, timeout: timeout}
}

// NewBackoffWithJitter returns a Waiter that rests a random time in [d/2, d]
// after the nth not-ready poll. d is delays[n-1], or the last delay once n
// passes the list. Non-positive values fall back to the defaults of New.
func NewBackoffWithJitter[T any](delays []time.Duration, timeout time.Duration) *Waiter[T] {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	schedule := make([]time.Duration, 0, max(len(delays), 1))
	for _, delay := range delays {
		if delay <= 0 {
			delay = defaultInterval
		}
		schedule = append(schedule, delay)
	}
	if len(schedule) == 0 {
		schedule = append(schedule, defaultInterval)
	}
	return &Waiter[T]{
		interval: schedule[0],
		timeout:  timeout,
		delays:   schedule,
		jitter:   equalJitter,
	}
}

// WaitFor calls poll until it returns a value with a nil error.
//
// The poll function classifies its own outcomes:
//   - (value, nil) means done and value is returned.
//   - (_, ErrNotReady) means keep polling; wrap it to record why.
//   - (_, any other error) means fatal and is returned as-is.
//
// A deadline on ctx that comes no later than the timeout of the waiter ends
// the wait instead. The wait then fails with ErrTimeout and the cause of the
// deadline, so that an operation deadline reads as a timeout.
func (w *Waiter[T]) WaitFor(
	ctx context.Context,
	poll func(context.Context) (T, error),
) (T, error) {
	var zero T

	// One deadline ends the wait. A second timer at the same instant would race
	// the deadline of ctx and make the error depend on which timer fires first.
	waitCtx, cancel := context.WithCancel(ctx)
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > w.timeout {
		cancel()
		waitCtx, cancel = context.WithTimeout(ctx, w.timeout)
	}
	defer cancel()

	timer := time.NewTimer(w.interval)
	defer timer.Stop()

	// The last reason the object was not ready is included in a timeout error.
	var lastErr error
	notReady := 0

	for {
		value, err := poll(waitCtx)

		switch {
		case err == nil:
			return value, nil
		case errors.Is(err, ErrNotReady):
			lastErr = err
			notReady++
		case waitCtx.Err() != nil:
			return zero, w.fail(ctx, lastErr)
		default:
			return zero, err
		}

		// Reset after poll returns so interval is the gap between calls rather
		// than a fixed schedule that a slow API can outrun.
		resetTimer(timer, w.rest(notReady))

		select {
		case <-waitCtx.Done():
			return zero, w.fail(ctx, lastErr)
		case <-timer.C:
		}
	}
}

// rest returns the gap after the nth not-ready poll.
func (w *Waiter[T]) rest(notReady int) time.Duration {
	if len(w.delays) == 0 {
		return w.interval
	}
	level := w.delays[min(notReady, len(w.delays))-1]
	return w.jitter(level)
}

func (w *Waiter[T]) fail(ctx context.Context, lastErr error) error {
	var err error
	switch ctxErr := ctx.Err(); {
	case ctxErr == nil:
		err = fmt.Errorf("%w after %s", ErrTimeout, w.timeout)
	case errors.Is(ctxErr, context.DeadlineExceeded):
		err = context.Cause(ctx)
		if !errors.Is(err, ErrTimeout) {
			err = fmt.Errorf("%w: %w", ErrTimeout, err)
		}
	default:
		return fmt.Errorf("wait cancelled: %w", ctxErr)
	}
	if lastErr != nil {
		return fmt.Errorf("%w: %w", err, lastErr)
	}
	return err
}

func resetTimer(timer *time.Timer, interval time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(interval)
}

// equalJitter returns a random duration in [level/2, level].
func equalJitter(level time.Duration) time.Duration {
	half := level / 2
	return half + rand.N(level-half+1) //nolint:gosec // jitter needs no secrecy
}
