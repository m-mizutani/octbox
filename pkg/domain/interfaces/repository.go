package interfaces

import (
	"context"
	"time"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

type Repository interface {
	// SaveItems stores open items of repositories that are not archived only;
	// any other item is rejected.
	SaveItems(ctx context.Context, items []model.Item) error
	DeleteItems(ctx context.Context, ids []model.ItemID) error
	AddCategory(ctx context.Context, category model.Category, ids []model.ItemID) error
	ReplaceCategory(ctx context.Context, category model.Category, ids []model.ItemID) error
	ListEntries(ctx context.Context) ([]*model.Entry, error)
	GetEntry(ctx context.Context, id model.ItemID) (*model.Entry, error) // ErrItemNotFound
	FindItemsByRefs(ctx context.Context, refs []model.ItemRef) (map[model.ItemRef]*model.Item, error)
	// ListRefreshIDs returns the pull requests with pending CI when
	// pendingOnly is true, and every stored item otherwise.
	ListRefreshIDs(ctx context.Context, pendingOnly bool) ([]model.ItemID, error)
	SetRead(ctx context.Context, marks map[model.ItemID]time.Time) error
	ClearRead(ctx context.Context, id model.ItemID) error
	// PurgeUncategorized deletes every item that belongs to no category.
	PurgeUncategorized(ctx context.Context) (int, error)
	GetSyncState(ctx context.Context) (*model.SyncState, error)
	PutSyncState(ctx context.Context, state *model.SyncState) error
	// ClaimSync sets LeaseUntil = now+lease and returns the state when
	// LeaseUntil <= now and (force || now >= NextRunAt).
	ClaimSync(ctx context.Context, now time.Time, lease time.Duration, force bool) (*model.SyncState, bool, error)
	// RenewSync moves LeaseUntil from held to until and reports true only
	// when LeaseUntil still equals held, i.e. no other process has claimed
	// the sync since.
	RenewSync(ctx context.Context, held, until time.Time) (bool, error)
	// ReleaseSync saves state only when LeaseUntil still equals held, and
	// reports whether it did.
	ReleaseSync(ctx context.Context, state *model.SyncState, held time.Time) (bool, error)
	ResetAll(ctx context.Context) error
	Close() error
}
