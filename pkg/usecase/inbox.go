package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// Inbox lists stored items and is the only component that opens items and
// records read marks and archive marks for the user.
type Inbox struct {
	repo    interfaces.Repository
	browser interfaces.Browser
}

func NewInbox(repo interfaces.Repository, browser interfaces.Browser) *Inbox {
	return &Inbox{repo: repo, browser: browser}
}

// List returns the entries that match filter, most recently updated first.
func (x *Inbox) List(ctx context.Context, filter model.Filter) ([]*model.Entry, error) {
	if err := filter.Scope.Validate(); err != nil {
		return nil, goerr.Wrap(err, "invalid filter")
	}
	entries, err := x.repo.ListEntries(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list entries")
	}
	matched := make([]*model.Entry, 0, len(entries))
	for _, e := range entries {
		if filter.Match(e) {
			matched = append(matched, e)
		}
	}
	return matched, nil
}

// Open opens the item in the browser and marks it read only when the browser
// opened.
func (x *Inbox) Open(ctx context.Context, id model.ItemID) error {
	entry, err := x.repo.GetEntry(ctx, id)
	if err != nil {
		return goerr.Wrap(err, "failed to get the item", goerr.V("item_id", id))
	}
	if err := x.browser.Open(ctx, entry.Item.URL); err != nil {
		return goerr.Wrap(err, "failed to open the item", goerr.V("item_id", id), goerr.T(TagBrowserFailed))
	}
	if err := x.repo.SetRead(ctx, map[model.ItemID]time.Time{id: entry.Item.UpdatedAt}); err != nil {
		return goerr.Wrap(err, "failed to mark the item read", goerr.V("item_id", id), goerr.T(TagOpenedNotMarked))
	}
	return nil
}

var (
	// TagBrowserFailed marks an Open that could not start the browser; the
	// item stays unread.
	TagBrowserFailed = goerr.NewTag("browser_failed")
	// TagOpenedNotMarked marks an Open that started the browser but could not
	// record the read mark.
	TagOpenedNotMarked = goerr.NewTag("opened_not_marked")
)

// ToggleRead marks an unread item read and a read item unread.
func (x *Inbox) ToggleRead(ctx context.Context, id model.ItemID) error {
	entry, err := x.repo.GetEntry(ctx, id)
	if err != nil {
		return goerr.Wrap(err, "failed to get the item", goerr.V("item_id", id))
	}
	if entry.Unread() {
		err = x.repo.SetRead(ctx, map[model.ItemID]time.Time{id: entry.Item.UpdatedAt})
	} else {
		err = x.repo.ClearRead(ctx, id)
	}
	if err != nil {
		return goerr.Wrap(err, "failed to toggle the read mark", goerr.V("item_id", id))
	}
	return nil
}

// ToggleArchive archives the item when it is not archived, recording
// seenUpdatedAt (the UpdatedAt the user saw, so that an update stored after
// the list was read keeps the item in the inbox), and removes the archive
// mark when it is archived. Read marks are not changed. It returns
// interfaces.ErrItemNotFound for an unknown ID.
func (x *Inbox) ToggleArchive(ctx context.Context, id model.ItemID, seenUpdatedAt time.Time) error {
	entry, err := x.repo.GetEntry(ctx, id)
	if err != nil {
		return goerr.Wrap(err, "failed to get the item", goerr.V("item_id", id))
	}
	if entry.Archived() {
		err = x.repo.ClearArchived(ctx, id)
	} else {
		err = x.repo.SetArchived(ctx, id, seenUpdatedAt)
	}
	if err != nil {
		return goerr.Wrap(err, "failed to toggle the archive mark", goerr.V("item_id", id))
	}
	return nil
}

// MarkRead marks every given item read. Unknown IDs (items deleted by a sync
// since they were listed) are ignored.
func (x *Inbox) MarkRead(ctx context.Context, ids []model.ItemID) error {
	marks := make(map[model.ItemID]time.Time, len(ids))
	for _, id := range ids {
		entry, err := x.repo.GetEntry(ctx, id)
		if errors.Is(err, interfaces.ErrItemNotFound) {
			continue
		}
		if err != nil {
			return goerr.Wrap(err, "failed to get the item", goerr.V("item_id", id))
		}
		marks[id] = entry.Item.UpdatedAt
	}
	if err := x.repo.SetRead(ctx, marks); err != nil {
		return goerr.Wrap(err, "failed to mark items read", goerr.V("count", len(marks)))
	}
	return nil
}

func (x *Inbox) SyncState(ctx context.Context) (*model.SyncState, error) {
	state, err := x.repo.GetSyncState(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the sync state")
	}
	return state, nil
}
