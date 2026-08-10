package layr8

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// These pin the behaviour every Layr8 SDK's watcher shares, so the Go side
// cannot drift from it silently.

// scripted answers one fetch call at a time; the last answer repeats.
type scripted struct {
	mu      sync.Mutex
	answers []any // []string or error
	calls   int
}

func (s *scripted) fetch(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	answer := s.answers[min(s.calls-1, len(s.answers)-1)]
	if err, ok := answer.(error); ok {
		return nil, err
	}
	return answer.([]string), nil
}

func (s *scripted) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type recorder struct {
	mu        sync.Mutex
	wallet    [][]string
	resources [][]string
	errs      []SpaceWatchSignal
}

func (r *recorder) onWallet(v []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wallet = append(r.wallet, v)
}

func (r *recorder) onResources(v []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resources = append(r.resources, v)
}

func (r *recorder) onError(signal SpaceWatchSignal, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, signal)
}

func (r *recorder) counts() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.wallet), len(r.resources), len(r.errs)
}

func newWatcher(wallet, resources *scripted, rec *recorder) *SpaceWatcher {
	return NewSpaceWatcher(SpaceWatcherOptions{
		FetchWallet:       wallet.fetch,
		FetchResources:    resources.fetch,
		OnWalletChange:    rec.onWallet,
		OnResourcesChange: rec.onResources,
		OnError:           rec.onError,
	})
}

func TestOrderIndependentSignature(t *testing.T) {
	if OrderIndependentSignature([]string{"b", "a"}) != OrderIndependentSignature([]string{"a", "b"}) {
		t.Error("order must not matter")
	}
	if OrderIndependentSignature([]string{"a", "a", "b"}) != OrderIndependentSignature([]string{"a", "b"}) {
		t.Error("duplicates must not matter")
	}
	// The empty string is what drives the resource empty-result debounce.
	if OrderIndependentSignature(nil) != "" {
		t.Error("empty must be the empty string")
	}
}

func TestAcceptsResourcePoll(t *testing.T) {
	if !acceptsResourcePoll(false, true, 0) {
		t.Error("anything non-empty applies at once")
	}
	if !acceptsResourcePoll(true, false, 1) {
		t.Error("empty applies when there was nothing to lose")
	}
	// A directory answering with nothing is just as likely to be a keepalive
	// blip as a real teardown, and acting on it strips every resource-derived
	// tool from every live session.
	if acceptsResourcePoll(true, true, 1) {
		t.Error("the first empty after a non-empty baseline must be ridden out")
	}
	if !acceptsResourcePoll(true, true, 2) {
		t.Error("two consecutive empties must be believed")
	}
}

func TestSpaceWatcher_FirstWalletPollSeedsTheBaselineSilently(t *testing.T) {
	// A cold start is not a change.
	rec := &recorder{}
	w := newWatcher(&scripted{answers: []any{[]string{"cred-1"}}}, &scripted{answers: []any{[]string{}}}, rec)

	w.RefreshWallet(context.Background())

	if n, _, _ := rec.counts(); n != 0 {
		t.Fatalf("wallet callbacks = %d, want 0", n)
	}
}

func TestSpaceWatcher_WalletChangeNotifiesWithTheFreshValue(t *testing.T) {
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"cred-1"}, []string{"cred-1", "cred-2"}}}
	w := newWatcher(wallet, &scripted{answers: []any{[]string{}}}, rec)

	w.RefreshWallet(context.Background())
	w.RefreshWallet(context.Background())

	if len(rec.wallet) != 1 || len(rec.wallet[0]) != 2 {
		t.Fatalf("wallet callbacks = %v", rec.wallet)
	}
}

func TestSpaceWatcher_SameSetDifferentOrderIsNotAChange(t *testing.T) {
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"a", "b"}, []string{"b", "a"}}}
	w := newWatcher(wallet, &scripted{answers: []any{[]string{}}}, rec)

	w.RefreshWallet(context.Background())
	w.RefreshWallet(context.Background())

	if n, _, _ := rec.counts(); n != 0 {
		t.Fatalf("wallet callbacks = %d, want 0", n)
	}
}

func TestSpaceWatcher_AnEmptyWalletIsARealAnswerAndNeverDebounces(t *testing.T) {
	// A wallet answering "nothing held" is a different failure shape from a
	// directory blip, and callers must be able to trust it immediately.
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"cred-1"}, []string{}}}
	w := newWatcher(wallet, &scripted{answers: []any{[]string{}}}, rec)

	w.RefreshWallet(context.Background())
	w.RefreshWallet(context.Background())

	if n, _, _ := rec.counts(); n != 1 {
		t.Fatalf("wallet callbacks = %d, want 1", n)
	}
}

func TestSpaceWatcher_AFetchErrorNeverWipesTheRetainedSignature(t *testing.T) {
	// A transient wallet-read failure must not read as "everything disappeared."
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"cred-1"}, errors.New("boom"), []string{"cred-1"}}}
	w := newWatcher(wallet, &scripted{answers: []any{[]string{}}}, rec)

	w.RefreshWallet(context.Background())
	w.RefreshWallet(context.Background())
	w.RefreshWallet(context.Background())

	walletCalls, _, errCount := rec.counts()
	if walletCalls != 0 {
		t.Errorf("wallet callbacks = %d, want 0", walletCalls)
	}
	if errCount != 1 {
		t.Errorf("error callbacks = %d, want 1", errCount)
	}
}

func TestSpaceWatcher_ResourceGrowthAppliesImmediately(t *testing.T) {
	rec := &recorder{}
	resources := &scripted{answers: []any{[]string{"a"}, []string{"a", "b"}}}
	w := newWatcher(&scripted{answers: []any{[]string{}}}, resources, rec)

	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background())

	if _, n, _ := rec.counts(); n != 1 {
		t.Fatalf("resource callbacks = %d, want 1", n)
	}
}

func TestSpaceWatcher_ShrinkingToStillNonEmptyAppliesImmediately(t *testing.T) {
	rec := &recorder{}
	resources := &scripted{answers: []any{[]string{"a", "b"}, []string{"a"}}}
	w := newWatcher(&scripted{answers: []any{[]string{}}}, resources, rec)

	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background())

	if _, n, _ := rec.counts(); n != 1 {
		t.Fatalf("resource callbacks = %d, want 1", n)
	}
}

func TestSpaceWatcher_OneEmptyResourcePollIsRiddenOut(t *testing.T) {
	rec := &recorder{}
	resources := &scripted{answers: []any{[]string{"a"}, []string{}, []string{"a"}}}
	w := newWatcher(&scripted{answers: []any{[]string{}}}, resources, rec)

	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background()) // empty — not believed yet
	if _, n, _ := rec.counts(); n != 0 {
		t.Fatalf("resource callbacks after one empty = %d, want 0", n)
	}
	w.RefreshResources(context.Background()) // came straight back
	if _, n, _ := rec.counts(); n != 0 {
		t.Fatalf("resource callbacks after recovery = %d, want 0", n)
	}
}

func TestSpaceWatcher_TwoConsecutiveEmptiesAreBelieved(t *testing.T) {
	rec := &recorder{}
	resources := &scripted{answers: []any{[]string{"a"}, []string{}, []string{}}}
	w := newWatcher(&scripted{answers: []any{[]string{}}}, resources, rec)

	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background())

	if _, n, _ := rec.counts(); n != 1 {
		t.Fatalf("resource callbacks = %d, want 1", n)
	}
}

func TestSpaceWatcher_AnErrorDoesNotCountTowardTheEmptyStreak(t *testing.T) {
	// An error is not an answer. Counting it would let one failed poll plus one
	// real empty tear down every resource-derived tool.
	rec := &recorder{}
	resources := &scripted{answers: []any{[]string{"a"}, errors.New("directory down"), []string{}}}
	w := newWatcher(&scripted{answers: []any{[]string{}}}, resources, rec)

	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background())
	w.RefreshResources(context.Background())

	if _, n, _ := rec.counts(); n != 0 {
		t.Fatalf("resource callbacks = %d, want 0", n)
	}
}

func TestSpaceWatcher_StartSeedsBothBaselinesWithoutNotifying(t *testing.T) {
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"a"}}}
	resources := &scripted{answers: []any{[]string{"b"}}}

	w := NewSpaceWatcher(SpaceWatcherOptions{
		FetchWallet: wallet.fetch, FetchResources: resources.fetch,
		OnWalletChange: rec.onWallet, OnResourcesChange: rec.onResources,
		WalletPollInterval: time.Hour, ResourcePollInterval: time.Hour,
	})
	w.Start(context.Background())
	defer w.Stop()

	if wallet.callCount() != 1 || resources.callCount() != 1 {
		t.Fatalf("calls: wallet=%d resources=%d", wallet.callCount(), resources.callCount())
	}
	if a, b, _ := rec.counts(); a != 0 || b != 0 {
		t.Fatalf("callbacks on seed: wallet=%d resources=%d", a, b)
	}
}

func TestSpaceWatcher_EachSignalPollsOnItsOwnInterval(t *testing.T) {
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"a"}}}
	resources := &scripted{answers: []any{[]string{"b"}}}

	w := NewSpaceWatcher(SpaceWatcherOptions{
		FetchWallet: wallet.fetch, FetchResources: resources.fetch,
		OnWalletChange: rec.onWallet, OnResourcesChange: rec.onResources,
		WalletPollInterval: 20 * time.Millisecond, ResourcePollInterval: time.Hour,
	})
	w.Start(context.Background())
	defer w.Stop()

	time.Sleep(150 * time.Millisecond)

	if wallet.callCount() <= 2 {
		t.Errorf("wallet polled %d times, expected several", wallet.callCount())
	}
	if resources.callCount() != 1 {
		t.Errorf("resources polled %d times, expected only its baseline", resources.callCount())
	}
}

func TestSpaceWatcher_StartIsIdempotent(t *testing.T) {
	rec := &recorder{}
	wallet := &scripted{answers: []any{[]string{"a"}}}
	w := NewSpaceWatcher(SpaceWatcherOptions{
		FetchWallet: wallet.fetch, FetchResources: (&scripted{answers: []any{[]string{"b"}}}).fetch,
		OnWalletChange:     rec.onWallet,
		WalletPollInterval: time.Hour, ResourcePollInterval: time.Hour,
	})

	w.Start(context.Background())
	w.Start(context.Background())
	defer w.Stop()

	if wallet.callCount() != 1 {
		t.Fatalf("wallet polled %d times after a double Start", wallet.callCount())
	}
}

func TestSpaceWatcher_StopEndsThePolling(t *testing.T) {
	wallet := &scripted{answers: []any{[]string{"a"}}}
	w := NewSpaceWatcher(SpaceWatcherOptions{
		FetchWallet: wallet.fetch, FetchResources: (&scripted{answers: []any{[]string{"b"}}}).fetch,
		WalletPollInterval: 20 * time.Millisecond, ResourcePollInterval: 20 * time.Millisecond,
	})

	w.Start(context.Background())
	w.Stop()
	afterStop := wallet.callCount()

	time.Sleep(80 * time.Millisecond)
	if wallet.callCount() != afterStop {
		t.Fatalf("polling continued after Stop: %d → %d", afterStop, wallet.callCount())
	}
}

func TestSpaceWatcher_StopWithoutStartIsSafe(t *testing.T) {
	NewSpaceWatcher(SpaceWatcherOptions{
		FetchWallet:    (&scripted{answers: []any{[]string{}}}).fetch,
		FetchResources: (&scripted{answers: []any{[]string{}}}).fetch,
	}).Stop()
}
