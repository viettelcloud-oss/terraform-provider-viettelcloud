// Package keylock serializes work on objects that share a key within one
// process, such as the operations that claim one backend object.
package keylock

import (
	"context"
	"slices"
	"sync"
)

// Locker holds one lock per key. The zero value is not ready; use New.
type Locker struct {
	mu    sync.Mutex
	slots map[string]*slot
}

// slot is the lock of one key. The key is held while held contains a token,
// and refs counts the callers that hold or wait for the key.
type slot struct {
	held chan struct{}
	refs int
}

// New returns a Locker with no held keys.
func New() *Locker {
	return &Locker{slots: map[string]*slot{}}
}

// Lock acquires every key and returns the function that releases them. It
// acquires the keys in ascending order, so two callers that lock overlapping
// keys cannot deadlock. A duplicate key counts once. When ctx ends first,
// Lock releases the keys that it already holds and returns ctx.Err().
func (l *Locker) Lock(ctx context.Context, keys ...string) (func(), error) {
	keys = slices.Compact(slices.Sorted(slices.Values(keys)))
	held := make([]string, 0, len(keys))
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			s := l.slot(held[i])
			<-s.held
			l.unref(held[i])
		}
	}
	for _, key := range keys {
		s := l.ref(key)
		select {
		case s.held <- struct{}{}:
			held = append(held, key)
		case <-ctx.Done():
			l.unref(key)
			release()
			return nil, ctx.Err()
		}
	}
	return sync.OnceFunc(release), nil
}

// ref returns the slot of key and counts one more caller on it.
func (l *Locker) ref(key string) *slot {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.slots[key]
	if s == nil {
		s = &slot{held: make(chan struct{}, 1)}
		l.slots[key] = s
	}
	s.refs++
	return s
}

// slot returns the slot of a key that the caller holds.
func (l *Locker) slot(key string) *slot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.slots[key]
}

// unref counts one caller less on key and drops the slot when no caller holds
// or waits for it.
func (l *Locker) unref(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.slots[key]
	s.refs--
	if s.refs == 0 {
		delete(l.slots, key)
	}
}
