package usecase

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/errutil"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

const (
	leaseDuration             = 2 * time.Minute
	leaseRenewMargin          = time.Minute
	searchOverlap             = 10 * time.Minute
	fullSyncInterval          = 6 * time.Hour
	openRefreshInterval       = 10 * time.Minute
	initialNotificationWindow = 30 * 24 * time.Hour
	lowRateThreshold          = 200
	maxSearchPages            = 20
	resolveChunk              = 50
	nodesChunk                = 100
	baseBackoff               = time.Minute
	maxBackoff                = 15 * time.Minute
	stateCheckInterval        = 5 * time.Second
)

// tagStorage marks errors returned by the repository, so a failed sync can
// be reported as a local storage failure.
var tagStorage = goerr.NewTag("storage")

// errLeaseLost stops a run whose lease another process has claimed after it
// expired. The run writes nothing more.
var errLeaseLost = errors.New("the sync lease was claimed by another process")

type searchSpec struct {
	category model.Category
	format   string
}

// searchSpecs are run in this order on every sync.
var searchSpecs = []searchSpec{
	{category: model.CategoryReview, format: "is:pr review-requested:%s"},
	{category: model.CategoryReview, format: "is:pr reviewed-by:%s"},
	{category: model.CategoryCreated, format: "author:%s"},
	{category: model.CategoryCommented, format: "commenter:%s"},
	{category: model.CategoryAssigned, format: "assignee:%s"},
}

// searchCategories are the categories maintained by search, in the order
// their memberships are written.
var searchCategories = []model.Category{
	model.CategoryReview, model.CategoryAssigned, model.CategoryCreated, model.CategoryCommented,
}

// Syncer decides what to fetch from GitHub and stores the results. It is the
// only component that manages the search cursor, the full sync period, the
// rate limit wait, and the lease shared by processes.
type Syncer struct {
	client        interfaces.GitHubClient
	repo          interfaces.Repository
	interval      time.Duration
	now           func() time.Time
	checkInterval time.Duration

	// viewer is the login of the token, asked once per process: the
	// database may hold another account's data (another --db-path, or a
	// login whose reset failed).
	viewer string
}

type SyncerOption func(*Syncer)

func WithClock(now func() time.Time) SyncerOption {
	return func(x *Syncer) { x.now = now }
}

func NewSyncer(client interfaces.GitHubClient, repo interfaces.Repository,
	interval time.Duration, opts ...SyncerOption) *Syncer {
	x := &Syncer{
		client:        client,
		repo:          repo,
		interval:      interval,
		now:           time.Now,
		checkInterval: stateCheckInterval,
	}
	for _, opt := range opts {
		opt(x)
	}
	return x
}

type SyncReport struct {
	Skipped bool
	Full    bool
	Saved   int // items saved in this run
	State   model.SyncState
}

type SyncEvent struct {
	Running bool
	State   model.SyncState
}

// syncRun holds what one RunOnce has done so far.
type syncRun struct {
	now          time.Time
	state        *model.SyncState
	held         time.Time // LeaseUntil this process holds
	first        bool
	full         bool
	saved        map[model.ItemID]time.Time
	deleted      map[model.ItemID]bool
	rate         model.RateLimit
	polledAt     time.Time // zero when the notifications API was not called
	pollInterval time.Duration
	requests     int
}

func (r *syncRun) observeRate(rl model.RateLimit) {
	r.requests++
	if rl.Remaining < 0 {
		return
	}
	if r.rate.Remaining < 0 || rl.Remaining < r.rate.Remaining {
		r.rate = rl
	}
}

func storageErr(err error, msg string, opts ...goerr.Option) error {
	return goerr.Wrap(err, msg, append(opts, goerr.T(tagStorage))...)
}

// checkpoint renews the lease when less than leaseRenewMargin is left. It is
// called before every GitHub request and before writing its result, so no
// write happens after another process could have claimed the sync.
func (x *Syncer) checkpoint(ctx context.Context, run *syncRun) error {
	now := x.now()
	if now.Add(leaseRenewMargin).Before(run.held) {
		return nil
	}
	until := now.Add(leaseDuration)
	ok, err := x.repo.RenewSync(ctx, run.held, until)
	if err != nil {
		return storageErr(err, "failed to renew the sync lease")
	}
	if !ok {
		return errLeaseLost
	}
	run.held = until
	run.state.LeaseUntil = until
	return nil
}

// RunOnce runs one sync when this process can claim it. On failure it
// returns the saved state in the report together with the error.
func (x *Syncer) RunOnce(ctx context.Context, force bool) (*SyncReport, error) {
	now := x.now()
	state, claimed, err := x.repo.ClaimSync(ctx, now, leaseDuration, force)
	if err != nil {
		return nil, storageErr(err, "failed to claim the sync")
	}
	if !claimed {
		return &SyncReport{Skipped: true, State: *state}, nil
	}

	run := &syncRun{
		now:     now,
		state:   state,
		held:    state.LeaseUntil,
		saved:   map[model.ItemID]time.Time{},
		deleted: map[model.ItemID]bool{},
		rate:    model.RateLimit{Remaining: -1},
	}
	// NextRunAt includes the last X-Poll-Interval, so a forced sync before
	// it skips the notifications API and must not move NextRunAt earlier.
	prevNextRunAt := state.NextRunAt
	pollNotifications := !now.Before(prevNextRunAt)

	syncErr := x.sync(ctx, run, pollNotifications)

	logging.From(ctx).Debug("sync finished",
		"full", run.full, "graphql_requests", run.requests, "saved", len(run.saved),
		"deleted", len(run.deleted), "rate_remaining", run.rate.Remaining, "error", syncErr)

	if errors.Is(syncErr, errLeaseLost) {
		logging.From(ctx).Warn("stopped a sync whose lease expired", "lease_until", run.held)
		return &SyncReport{Skipped: true, State: *state}, nil
	}

	state.LeaseUntil = time.Time{}
	state.LastSyncAt = now
	if run.rate.Remaining >= 0 {
		state.RateRemaining = run.rate.Remaining
		state.RateResetAt = run.rate.ResetAt
	}

	if syncErr != nil && run.first {
		// The first sync did not reach its read baseline: the next run starts
		// over as a first sync.
		state.SearchCursor = time.Time{}
		state.NotificationCursor = time.Time{}
		state.NotificationLastModified = ""
		state.LastFullSyncAt = time.Time{}
		state.LastOpenRefreshAt = time.Time{}
	}

	switch {
	case syncErr != nil && ctx.Err() != nil:
		// Quitting is not a failure of GitHub or the network; only the lease
		// is released, so another process does not wait for it to expire.
		x.release(ctx, run, state)
		return &SyncReport{State: *state}, goerr.Wrap(syncErr, "sync was cancelled")

	case syncErr != nil:
		state.ConsecutiveFailures++
		state.LastError = classify(syncErr)
		wait := backoff(state.ConsecutiveFailures)
		var rle *interfaces.RateLimitedError
		if errors.As(syncErr, &rle) {
			wait = rle.RetryAfter
		}
		// GitHub's wait counts from its response, not from the start of the
		// run.
		state.NextRunAt = x.now().Add(wait)

	default:
		state.ConsecutiveFailures = 0
		state.LastError = model.SyncErrorNone
		state.NextRunAt = now.Add(x.interval)
		if state.RateRemaining >= 0 && state.RateRemaining < lowRateThreshold {
			state.NextRunAt = laterOf(state.NextRunAt, state.RateResetAt)
		}
	}

	if run.polledAt.IsZero() {
		state.NextRunAt = laterOf(state.NextRunAt, prevNextRunAt)
	} else {
		state.NextRunAt = laterOf(state.NextRunAt, run.polledAt.Add(run.pollInterval))
	}

	if syncErr != nil {
		x.release(ctx, run, state)
		return &SyncReport{Full: run.full, Saved: len(run.saved), State: *state}, goerr.Wrap(syncErr, "failed to sync")
	}

	released, err := x.repo.ReleaseSync(context.WithoutCancel(ctx), state, run.held)
	if err != nil {
		return nil, storageErr(err, "failed to save the sync state")
	}
	if !released {
		logging.From(ctx).Warn("did not save the sync state: the lease expired", "lease_until", run.held)
		return &SyncReport{Skipped: true, State: *state}, nil
	}
	return &SyncReport{Full: run.full, Saved: len(run.saved), State: *state}, nil
}

// release saves the state of a failed or cancelled run and records, instead
// of returning, a failure to save it: the caller returns the sync error.
func (x *Syncer) release(ctx context.Context, run *syncRun, state *model.SyncState) {
	released, err := x.repo.ReleaseSync(context.WithoutCancel(ctx), state, run.held)
	if err != nil {
		errutil.Handle(ctx, storageErr(err, "failed to save the sync state"), "failed to save the sync state")
		return
	}
	if !released {
		logging.From(ctx).Warn("did not save the sync state: the lease expired", "lease_until", run.held)
	}
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func backoff(failures int) time.Duration {
	wait := baseBackoff
	for i := 1; i < failures; i++ {
		wait *= 2
		if wait >= maxBackoff {
			return maxBackoff
		}
	}
	return wait
}

func classify(err error) model.SyncError {
	var (
		rle    *interfaces.RateLimitedError
		urlErr *url.Error
		netErr net.Error
		apiErr *interfaces.APIError
	)
	switch {
	case errors.Is(err, interfaces.ErrUnauthorized):
		return model.SyncErrorUnauthorized
	case errors.As(err, &rle):
		return model.SyncErrorRateLimited
	case errors.As(err, &urlErr), errors.As(err, &netErr):
		return model.SyncErrorNetwork
	case errors.As(err, &apiErr):
		return model.SyncErrorAPI
	case goerr.HasTag(err, tagStorage):
		return model.SyncErrorStorage
	default:
		return model.SyncErrorUnknown
	}
}

// checkAccount makes sure the stored data belongs to the token's account,
// clearing it when it does not.
func (x *Syncer) checkAccount(ctx context.Context, run *syncRun) error {
	if x.viewer == "" {
		if err := x.checkpoint(ctx, run); err != nil {
			return err
		}
		viewer, err := x.client.Viewer(ctx)
		if err != nil {
			return goerr.Wrap(err, "failed to get the viewer")
		}
		x.viewer = viewer.Login
	}

	state := run.state
	if state.Viewer != "" && state.Viewer != x.viewer {
		logging.From(ctx).Info("the database holds another account's data; clearing it",
			"stored", state.Viewer, "token", x.viewer)
		if err := x.checkpoint(ctx, run); err != nil {
			return err
		}
		if err := x.repo.ResetAll(ctx); err != nil {
			return storageErr(err, "failed to clear another account's data")
		}
		// ResetAll also cleared the lease: claim it again before writing.
		fresh, claimed, err := x.repo.ClaimSync(ctx, x.now(), leaseDuration, true)
		if err != nil {
			return storageErr(err, "failed to claim the sync")
		}
		if !claimed {
			return errLeaseLost
		}
		*state = *fresh
		run.held = fresh.LeaseUntil
	}
	state.Viewer = x.viewer
	return nil
}

func (x *Syncer) sync(ctx context.Context, run *syncRun, pollNotifications bool) error {
	if err := x.checkAccount(ctx, run); err != nil {
		return err
	}
	state := run.state

	run.first = state.SearchCursor.IsZero()
	run.full = run.first || run.now.Sub(state.LastFullSyncAt) >= fullSyncInterval

	if err := x.search(ctx, run); err != nil {
		return err
	}
	if pollNotifications {
		if err := x.notifications(ctx, run); err != nil {
			return err
		}
	}
	if err := x.refresh(ctx, run); err != nil {
		return err
	}

	if err := x.checkpoint(ctx, run); err != nil {
		return err
	}
	if run.first {
		if err := x.markAllRead(ctx); err != nil {
			return err
		}
	}
	if run.full {
		n, err := x.repo.PurgeUncategorized(ctx)
		if err != nil {
			return storageErr(err, "failed to purge uncategorized items")
		}
		if n > 0 {
			logging.From(ctx).Debug("purged uncategorized items", "count", n)
		}
	}
	return nil
}

// markAllRead sets the read baseline of the first sync: every stored item,
// including those saved by an earlier first sync that failed halfway.
func (x *Syncer) markAllRead(ctx context.Context) error {
	entries, err := x.repo.ListEntries(ctx)
	if err != nil {
		return storageErr(err, "failed to list items for the read baseline")
	}
	marks := make(map[model.ItemID]time.Time, len(entries))
	for _, e := range entries {
		marks[e.Item.ID] = e.Item.UpdatedAt
	}
	if err := x.repo.SetRead(ctx, marks); err != nil {
		return storageErr(err, "failed to mark the first sync read")
	}
	return nil
}

// store saves the open items and deletes the closed or merged ones, and
// returns the open items.
func (x *Syncer) store(ctx context.Context, run *syncRun, items []model.Item) ([]model.Item, error) {
	if err := x.checkpoint(ctx, run); err != nil {
		return nil, err
	}
	var (
		open   []model.Item
		closed []model.ItemID
	)
	for _, item := range items {
		if item.State == model.ItemStateOpen {
			open = append(open, item)
		} else {
			closed = append(closed, item.ID)
		}
	}
	if err := x.repo.SaveItems(ctx, open); err != nil {
		return nil, storageErr(err, "failed to save items", goerr.V("count", len(open)))
	}
	if err := x.deleteItems(ctx, run, closed); err != nil {
		return nil, err
	}
	for _, item := range open {
		run.saved[item.ID] = item.UpdatedAt
		delete(run.deleted, item.ID)
	}
	return open, nil
}

func (x *Syncer) deleteItems(ctx context.Context, run *syncRun, ids []model.ItemID) error {
	if len(ids) == 0 {
		return nil
	}
	if err := x.repo.DeleteItems(ctx, ids); err != nil {
		return storageErr(err, "failed to delete items", goerr.V("count", len(ids)))
	}
	for _, id := range ids {
		run.deleted[id] = true
		delete(run.saved, id)
	}
	return nil
}

func (x *Syncer) search(ctx context.Context, run *syncRun) error {
	state := run.state
	cond := "is:open"
	if !run.full {
		cond = "updated:>=" + state.SearchCursor.Add(-searchOverlap).UTC().Format("2006-01-02T15:04:05Z")
	}

	members := map[model.Category][]model.ItemID{}
	for _, spec := range searchSpecs {
		query := fmt.Sprintf(spec.format, state.Viewer) + " archived:false " + cond + " sort:updated-desc"
		after := ""
		for page := 1; ; page++ {
			if err := x.checkpoint(ctx, run); err != nil {
				return err
			}
			result, err := x.client.SearchItems(ctx, query, after)
			if err != nil {
				return goerr.Wrap(err, "failed to search", goerr.V("query", query), goerr.V("page", page))
			}
			run.observeRate(result.RateLimit)
			open, err := x.store(ctx, run, result.Items)
			if err != nil {
				return err
			}
			for _, item := range open {
				members[spec.category] = append(members[spec.category], item.ID)
			}
			if !result.HasNextPage {
				break
			}
			if page == maxSearchPages {
				logging.From(ctx).Warn("search results exceed the limit of 1,000 items",
					"query", query, "category", spec.category)
				break
			}
			after = result.EndCursor
		}
	}

	if err := x.checkpoint(ctx, run); err != nil {
		return err
	}
	for _, category := range searchCategories {
		ids := x.alive(run, members[category])
		var err error
		if run.full {
			err = x.repo.ReplaceCategory(ctx, category, ids)
		} else {
			err = x.repo.AddCategory(ctx, category, ids)
		}
		if err != nil {
			return storageErr(err, "failed to update the category", goerr.V("category", category))
		}
	}

	state.SearchCursor = run.now
	if run.full {
		state.LastFullSyncAt = run.now
	}
	return nil
}

// alive drops IDs that a later step of the run deleted, and duplicates.
func (x *Syncer) alive(run *syncRun, ids []model.ItemID) []model.ItemID {
	seen := make(map[model.ItemID]bool, len(ids))
	result := make([]model.ItemID, 0, len(ids))
	for _, id := range ids {
		if run.deleted[id] || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func (x *Syncer) notifications(ctx context.Context, run *syncRun) error {
	state := run.state
	since := state.NotificationCursor.Add(-searchOverlap)
	if state.NotificationCursor.IsZero() {
		since = run.now.Add(-initialNotificationWindow)
	}

	if err := x.checkpoint(ctx, run); err != nil {
		return err
	}
	page, err := x.client.ListNotifications(ctx, since, state.NotificationLastModified)
	if err != nil {
		return goerr.Wrap(err, "failed to list notifications")
	}
	run.polledAt = x.now()
	run.pollInterval = page.PollInterval

	if !page.NotModified {
		latest := map[model.ItemRef]time.Time{}
		var refs []model.ItemRef
		for _, th := range page.Threads {
			prev, ok := latest[th.Ref]
			if !ok {
				refs = append(refs, th.Ref)
			}
			if !ok || th.UpdatedAt.After(prev) {
				latest[th.Ref] = th.UpdatedAt
			}
		}

		existing, err := x.repo.FindItemsByRefs(ctx, refs)
		if err != nil {
			return storageErr(err, "failed to find notified items")
		}

		var need []model.ItemRef
		for _, ref := range refs {
			item, ok := existing[ref]
			if !ok || latest[ref].After(item.UpdatedAt) {
				need = append(need, ref)
			}
		}

		var ids []model.ItemID
		fetched := map[model.ItemRef]bool{}
		for start := 0; start < len(need); start += resolveChunk {
			chunk := need[start:min(start+resolveChunk, len(need))]
			if err := x.checkpoint(ctx, run); err != nil {
				return err
			}
			result, err := x.client.ResolveItems(ctx, chunk)
			if err != nil {
				return goerr.Wrap(err, "failed to resolve notified items", goerr.V("count", len(chunk)))
			}
			run.observeRate(result.RateLimit)
			for _, item := range result.Items {
				fetched[item.Ref()] = true
			}
			open, err := x.store(ctx, run, result.Items)
			if err != nil {
				return err
			}
			for _, item := range open {
				ids = append(ids, item.ID)
			}
		}
		// A fetched item supersedes the stored one: its ID may have changed
		// (SaveItems then removed the old row) or it may be closed.
		for _, ref := range refs {
			if item, ok := existing[ref]; ok && !fetched[ref] {
				ids = append(ids, item.ID)
			}
		}

		if err := x.repo.AddCategory(ctx, model.CategorySubscribed, x.alive(run, ids)); err != nil {
			return storageErr(err, "failed to update the subscribed category")
		}
		state.NotificationLastModified = page.LastModified
	}

	// A 304 means nothing changed since the last Last-Modified, so moving the
	// cursor loses no notification.
	state.NotificationCursor = run.now
	return nil
}

func (x *Syncer) refresh(ctx context.Context, run *syncRun) error {
	state := run.state
	pendingOnly := !(run.full || run.now.Sub(state.LastOpenRefreshAt) >= openRefreshInterval)

	all, err := x.repo.ListPullRequestIDs(ctx, pendingOnly)
	if err != nil {
		return storageErr(err, "failed to list pull requests")
	}
	ids := make([]model.ItemID, 0, len(all))
	for _, id := range all {
		if _, ok := run.saved[id]; !ok {
			ids = append(ids, id)
		}
	}

	for start := 0; start < len(ids); start += nodesChunk {
		chunk := ids[start:min(start+nodesChunk, len(ids))]
		if err := x.checkpoint(ctx, run); err != nil {
			return err
		}
		result, err := x.client.GetItems(ctx, chunk)
		if err != nil {
			return goerr.Wrap(err, "failed to refresh pull requests", goerr.V("count", len(chunk)))
		}
		run.observeRate(result.RateLimit)
		if _, err := x.store(ctx, run, result.Items); err != nil {
			return err
		}
		if err := x.deleteItems(ctx, run, result.MissingIDs); err != nil {
			return err
		}
	}

	if !pendingOnly {
		state.LastOpenRefreshAt = run.now
	}
	return nil
}

// Run syncs whenever the schedule or trigger asks for it, until ctx is done.
// It returns nil when ctx is done, or ErrUnauthorized after notifying it.
func (x *Syncer) Run(ctx context.Context, trigger <-chan struct{}, notify func(SyncEvent)) error {
	ticker := time.NewTicker(x.checkInterval)
	defer ticker.Stop()

	var (
		lastSeen time.Time
		last     model.SyncState
		// requested is a trigger not yet served because another process held
		// the lease; it runs as soon as the lease is free.
		requested bool
	)
	if st, err := x.repo.GetSyncState(ctx); err == nil {
		last = *st
	}

	runOnce := func(force bool) error {
		notify(SyncEvent{Running: true, State: last})
		report, err := x.RunOnce(ctx, force)
		if report != nil {
			last = report.State
			if !report.Skipped {
				lastSeen = report.State.LastSyncAt
			}
			requested = force && report.Skipped
		}
		notify(SyncEvent{State: last})
		switch {
		case err == nil, ctx.Err() != nil:
			return nil
		case errors.Is(err, interfaces.ErrUnauthorized):
			// Returned, so the caller records it.
			return goerr.Wrap(err, "stopped syncing")
		}
		var rle *interfaces.RateLimitedError
		if errors.As(err, &rle) {
			err = goerr.Wrap(err, "waiting for the GitHub rate limit", goerr.T(errutil.TagBenign))
		}
		errutil.Handle(ctx, err, "sync failed")
		return nil
	}

	if err := runOnce(false); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil

		case <-trigger:
			if err := runOnce(true); err != nil {
				return err
			}

		case <-ticker.C:
			state, err := x.repo.GetSyncState(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				errutil.Handle(ctx, storageErr(err, "failed to read the sync state"), "failed to read the sync state")
				continue
			}
			if !state.LastSyncAt.Equal(lastSeen) {
				// Another process has synced.
				lastSeen = state.LastSyncAt
				last = *state
				notify(SyncEvent{State: last})
			}
			now := x.now()
			if now.Before(state.LeaseUntil) || (!requested && now.Before(state.NextRunAt)) {
				continue
			}
			if err := runOnce(requested); err != nil {
				return err
			}
		}
	}
}
