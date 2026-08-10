package layr8

// Space watch — poll, diff and notify on "does my MCP tool surface still look
// the same".
//
// Cross-language contract: contracts/sdk-space-watch.md. @layr8/sdk's
// SpaceWatcher (src/space-watch.ts) and the layr8 hex package's
// Layr8.SpaceWatcher are the same abstraction in their languages; all three
// exist so a caller sees a change at the same latency regardless of which SDK
// it is built on.
//
// Two independent signals, both POLLED — nothing on the wire tells an SDK "your
// wallet changed" or "a resource came up", and that absence is the reason this
// exists at all: since it is on us to notice, everyone should notice on the same
// terms.
//
//   - Wallet — the caller's held VG/credential set. A grant minted or revoked in
//     the portal changes this. Polled every 15s by default.
//   - Resources — the Space directory's live MCP Instance cards. An mcp-pod
//     registering or losing a directory card changes this. Polled every 60s.
//
// What a "change" MEANS to do about it stays entirely a consumer decision; this
// file owns poll, diff and debounce and nothing else. It never inspects the
// shape of what it fetched — the fetch functions return a flat []string of
// whatever the caller's domain calls an identifier, so there is no dependency on
// cloud-node's credential shape or discovery-service's card shape.

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default poll intervals, from the cross-language contract.
const (
	DefaultWalletPollInterval   = 15 * time.Second
	DefaultResourcePollInterval = 60 * time.Second
)

// SpaceWatchSignal names which of the two signals a callback concerns.
type SpaceWatchSignal string

const (
	WalletSignal    SpaceWatchSignal = "wallet"
	ResourcesSignal SpaceWatchSignal = "resources"
)

// SpaceWatcherOptions configures a SpaceWatcher. FetchWallet and FetchResources
// are required.
type SpaceWatcherOptions struct {
	// FetchWallet returns the caller's current credential identifiers.
	FetchWallet func(ctx context.Context) ([]string, error)
	// FetchResources returns the caller's current resource identifiers
	// (directory search, kind: service, :mcp: DIDs).
	FetchResources func(ctx context.Context) ([]string, error)

	// OnWalletChange is called with the new value when the wallet's signature
	// changes. Never called on the first successful poll — that seeds the
	// baseline silently, because a cold start is not a change.
	OnWalletChange func(wallet []string)
	// OnResourcesChange is the same for resources, after the empty-result
	// debounce.
	OnResourcesChange func(resources []string)

	// OnError is called (not returned) when a fetch fails, so the consumer can
	// log it. The watcher always retains the last-accepted signature and retries
	// next poll — a transient fetch failure must never read as "everything
	// disappeared."
	OnError func(signal SpaceWatchSignal, err error)

	// WalletPollInterval defaults to DefaultWalletPollInterval.
	WalletPollInterval time.Duration
	// ResourcePollInterval defaults to DefaultResourcePollInterval.
	ResourcePollInterval time.Duration
}

// OrderIndependentSignature is the sorted, deduped, comma-joined identity of a
// set of ids.
func OrderIndependentSignature(items []string) string {
	seen := make(map[string]struct{}, len(items))
	unique := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		unique = append(unique, item)
	}
	sort.Strings(unique)
	return strings.Join(unique, ",")
}

// acceptsResourcePoll decides whether to take this resource poll or ride out a
// possibly-transient empty result.
//
// A directory answering with nothing is not an error, but it is just as likely
// to be a momentary blip (a keepalive miss evicting a card that comes straight
// back) as a real teardown, and acting on it strips every resource-derived tool
// from every live session. Anything non-empty applies at once; so does an empty
// result when there was nothing to lose. Ported from the broker's
// acceptsDiscovery.
func acceptsResourcePoll(isEmpty, hadResources bool, emptyStreak int) bool {
	return !isEmpty || !hadResources || emptyStreak >= 2
}

// SpaceWatcher watches a wallet and a resource set on independent intervals,
// diffs each against its own last-accepted signature, and calls back on a real
// change.
//
// Resources debounce an empty result; the wallet does not. A wallet answering
// "nothing held" is a real answer, not a blip, and callers must be able to trust
// it immediately.
type SpaceWatcher struct {
	opts SpaceWatcherOptions

	mu                 sync.Mutex
	lastWalletSig      *string
	lastResourceSig    *string
	resourceEmptyCount int

	started bool
	cancel  context.CancelFunc
	done    sync.WaitGroup
}

// NewSpaceWatcher builds a watcher. Poll intervals left at zero take the
// contract defaults.
func NewSpaceWatcher(opts SpaceWatcherOptions) *SpaceWatcher {
	if opts.WalletPollInterval <= 0 {
		opts.WalletPollInterval = DefaultWalletPollInterval
	}
	if opts.ResourcePollInterval <= 0 {
		opts.ResourcePollInterval = DefaultResourcePollInterval
	}
	return &SpaceWatcher{opts: opts}
}

// Start seeds both baselines synchronously, then polls each on its own interval
// until Stop is called or ctx is cancelled. Calling Start twice is a no-op.
func (w *SpaceWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	// Seeding before the tickers start is what makes "the first poll is not a
	// change" observable to the caller: by the time Start returns, both
	// baselines are in place.
	w.RefreshWallet(runCtx)
	w.RefreshResources(runCtx)

	w.done.Add(2)
	go w.loop(runCtx, w.opts.WalletPollInterval, w.walletTick)
	go w.loop(runCtx, w.opts.ResourcePollInterval, w.resourceTick)
}

// Stop ends the polling and waits for both loops to exit. Safe when never
// started.
func (w *SpaceWatcher) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.started = false
	cancel := w.cancel
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	w.done.Wait()
}

// RefreshWallet forces an immediate out-of-cycle wallet check, e.g. right after
// minting a grant, without disturbing the regular interval.
func (w *SpaceWatcher) RefreshWallet(ctx context.Context) { w.walletTick(ctx) }

// RefreshResources forces an immediate out-of-cycle resource check.
func (w *SpaceWatcher) RefreshResources(ctx context.Context) { w.resourceTick(ctx) }

func (w *SpaceWatcher) loop(ctx context.Context, interval time.Duration, tick func(context.Context)) {
	defer w.done.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick(ctx)
		}
	}
}

func (w *SpaceWatcher) walletTick(ctx context.Context) {
	value, err := w.opts.FetchWallet(ctx)
	if err != nil {
		if w.opts.OnError != nil {
			w.opts.OnError(WalletSignal, err)
		}
		return // retain last-accepted signature; retry next poll
	}

	sig := OrderIndependentSignature(value)

	w.mu.Lock()
	isFirst := w.lastWalletSig == nil
	changed := !isFirst && sig != *w.lastWalletSig
	w.lastWalletSig = &sig // wallet never debounces empty
	w.mu.Unlock()

	if changed && w.opts.OnWalletChange != nil {
		w.opts.OnWalletChange(value)
	}
}

func (w *SpaceWatcher) resourceTick(ctx context.Context) {
	value, err := w.opts.FetchResources(ctx)
	if err != nil {
		if w.opts.OnError != nil {
			w.opts.OnError(ResourcesSignal, err)
		}
		return // retain last-accepted signature; retry next poll
	}

	sig := OrderIndependentSignature(value)
	isEmpty := sig == ""

	w.mu.Lock()
	if isEmpty {
		w.resourceEmptyCount++
	} else {
		w.resourceEmptyCount = 0
	}
	hadResources := w.lastResourceSig != nil && *w.lastResourceSig != ""

	if !acceptsResourcePoll(isEmpty, hadResources, w.resourceEmptyCount) {
		w.mu.Unlock()
		return // ride out one empty blip; last-accepted signature is untouched
	}

	isFirst := w.lastResourceSig == nil
	if !isFirst && sig == *w.lastResourceSig {
		w.mu.Unlock()
		return
	}
	w.lastResourceSig = &sig
	w.mu.Unlock()

	if !isFirst && w.opts.OnResourcesChange != nil {
		w.opts.OnResourcesChange(value)
	}
}
