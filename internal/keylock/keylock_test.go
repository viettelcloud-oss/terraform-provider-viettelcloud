package keylock

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestLockSerializesOneKey(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := New()
		unlock, err := l.Lock(context.Background(), "lb")
		if err != nil {
			t.Fatalf("first lock: %v", err)
		}
		acquired := make(chan struct{})
		go func() {
			second, err := l.Lock(context.Background(), "lb")
			if err != nil {
				t.Errorf("second lock: %v", err)
				return
			}
			close(acquired)
			second()
		}()
		synctest.Wait()
		select {
		case <-acquired:
			t.Fatal("a second caller acquired a key that the first caller still holds")
		default:
		}
		unlock()
		<-acquired
		synctest.Wait()
		if len(l.slots) != 0 {
			t.Fatalf("slots=%d after every caller released, want 0", len(l.slots))
		}
	})
}

func TestLockKeepsOtherKeysFree(t *testing.T) {
	t.Parallel()
	l := New()
	unlockA, err := l.Lock(context.Background(), "a")
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}
	defer unlockA()
	unlockB, err := l.Lock(context.Background(), "b")
	if err != nil {
		t.Fatalf("lock b while a is held: %v", err)
	}
	unlockB()
}

// Two callers that lock the same keys in opposite argument order would
// deadlock without the ascending acquisition order.
func TestLockAcquiresKeysInAscendingOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := New()
		done := make(chan struct{}, 2)
		for _, keys := range [][]string{{"server", "eip"}, {"eip", "server"}} {
			go func() {
				for range 100 {
					unlock, err := l.Lock(context.Background(), keys...)
					if err != nil {
						t.Errorf("lock %v: %v", keys, err)
						return
					}
					unlock()
				}
				done <- struct{}{}
			}()
		}
		<-done
		<-done
	})
}

func TestLockCountsADuplicateKeyOnce(t *testing.T) {
	t.Parallel()
	l := New()
	unlock, err := l.Lock(context.Background(), "lb", "lb")
	if err != nil {
		t.Fatalf("lock with a duplicate key: %v", err)
	}
	unlock()
	unlock()
	if len(l.slots) != 0 {
		t.Fatalf("slots=%d after release, want 0", len(l.slots))
	}
}

func TestLockReleasesHeldKeysWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := New()
		unlockB, err := l.Lock(context.Background(), "b")
		if err != nil {
			t.Fatalf("lock b: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := l.Lock(ctx, "a", "b")
			result <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled lock err=%v, want context.Canceled", err)
		}
		unlockA, err := l.Lock(context.Background(), "a")
		if err != nil {
			t.Fatalf("a must be free after the cancelled caller gave it back: %v", err)
		}
		unlockA()
		unlockB()
		if len(l.slots) != 0 {
			t.Fatalf("slots=%d after every caller released, want 0", len(l.slots))
		}
	})
}
