package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
)

var errPageFailed = errors.New("page failed")

// fakeSeq yields items and then err, the way an SDK list iterator reports a
// failed page after the pages that succeeded.
func fakeSeq[T any](items []*T, err error) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
		if err != nil {
			yield(nil, err)
		}
	}
}

// assertCardinality runs the zero, one, ambiguous, and SDK error cases that
// every finder shares. resolve is called with the listed items and error.
func assertCardinality[T any](t *testing.T, match, prefix *T, resolve func(items []*T, err error) (T, error), id func(T) string) {
	t.Helper()
	got, err := resolve([]*T{prefix, match}, nil)
	if err != nil || id(got) != id(*match) {
		t.Fatalf("exact match: got %s, %v", id(got), err)
	}
	if _, err := resolve([]*T{prefix}, nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a candidate that only contains the criterion must not match: %v", err)
	}
	if _, err := resolve(nil, nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("zero candidates: %v", err)
	}
	if _, err := resolve([]*T{match, match}, nil); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("multiple candidates: %v", err)
	}
	if _, err := resolve([]*T{match}, errPageFailed); !errors.Is(err, errPageFailed) {
		t.Fatalf("SDK error: %v", err)
	}
}

func background() context.Context { return context.Background() }
