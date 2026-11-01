package usecase

import (
	"context"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/utils/errutil"
)

// UnreadCountReporter shows the number of unread items on an
// UnreadCountDisplay. It reads the count from the database at every interval
// instead of being told about changes, so read marks written by the inbox,
// by a sync, or by another octbox process sharing the database all appear.
type UnreadCountReporter struct {
	repo     interfaces.Repository
	display  interfaces.UnreadCountDisplay
	interval time.Duration
}

func NewUnreadCountReporter(repo interfaces.Repository, display interfaces.UnreadCountDisplay, interval time.Duration) *UnreadCountReporter {
	return &UnreadCountReporter{repo: repo, display: display, interval: interval}
}

// Run shows the count now and at every interval until ctx is done. A failure
// is recorded only when the previous attempt succeeded, so an unreachable
// display does not write a log line at every interval.
func (x *UnreadCountReporter) Run(ctx context.Context) {
	ticker := time.NewTicker(x.interval)
	defer ticker.Stop()

	failing := false
	for {
		err := x.Report(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil && !failing {
			errutil.Handle(ctx, err, "failed to show the unread count")
		}
		failing = err != nil

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Report counts the unread items the inbox lists (Entry.InInbox) and shows
// the count once.
func (x *UnreadCountReporter) Report(ctx context.Context) error {
	entries, err := x.repo.ListEntries(ctx)
	if err != nil {
		return storageErr(err, "failed to list items to count unread ones")
	}
	count := 0
	for _, e := range entries {
		if e.Unread() && e.InInbox() {
			count++
		}
	}
	if err := x.display.ShowUnreadCount(ctx, count); err != nil {
		return goerr.Wrap(err, "failed to show the unread count", goerr.V("count", count))
	}
	return nil
}

// Stop puts back the names the count replaced. Call it after Run returns.
func (x *UnreadCountReporter) Stop(ctx context.Context) error {
	if err := x.display.RemoveUnreadCount(ctx); err != nil {
		return goerr.Wrap(err, "failed to remove the unread count")
	}
	return nil
}
