package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

// UpdateDetector finds the items that became unread since the previous check
// and shows them on an UpdateIndicator. Start runs before the syncer starts,
// Check only on the syncer goroutine, and Stop after the syncer stops, so
// known needs no lock.
type UpdateDetector struct {
	repo      interfaces.Repository
	indicator interfaces.UpdateIndicator
	// known maps each stored item to its updatedAt at the previous check,
	// except items whose review by the viewer was done (Item.ViewerReviewDone):
	// such an item counts as new when a review is requested from the viewer
	// again. nil until a list has been read once.
	known map[model.ItemID]time.Time
}

func NewUpdateDetector(repo interfaces.Repository, indicator interfaces.UpdateIndicator) *UpdateDetector {
	return &UpdateDetector{repo: repo, indicator: indicator}
}

// Start records the stored items as already known, then registers on the
// indicator. Registering is attempted even when reading fails; the errors
// are joined.
func (x *UpdateDetector) Start(ctx context.Context) error {
	var errs []error
	if current, err := x.listUpdatedAt(ctx); err != nil {
		errs = append(errs, err)
	} else {
		x.known = current
	}
	if err := x.indicator.Register(ctx); err != nil {
		errs = append(errs, goerr.Wrap(err, "failed to register on the update indicator"))
	}
	return errors.Join(errs...)
}

// Check compares the stored items with the known ones and calls ShowUpdate
// once when at least one unread item has an ID and updatedAt pair that was
// not known. known is replaced even when ShowUpdate fails, so the same
// update is not shown again on the next check.
func (x *UpdateDetector) Check(ctx context.Context) error {
	entries, err := x.repo.ListEntries(ctx)
	if err != nil {
		return storageErr(err, "failed to list items to check for updates")
	}
	current := updatedAtOf(entries)
	if x.known == nil {
		x.known = current
		return nil
	}

	updated := 0
	for _, e := range entries {
		if e.Item.ViewerReviewDone() {
			continue
		}
		prev, ok := x.known[e.Item.ID]
		if e.Unread() && (!ok || !prev.Equal(e.Item.UpdatedAt)) {
			updated++
		}
	}
	x.known = current
	if updated == 0 {
		return nil
	}

	logging.From(ctx).Debug("found new updates", "count", updated)
	if err := x.indicator.ShowUpdate(ctx); err != nil {
		return goerr.Wrap(err, "failed to show new updates", goerr.V("count", updated))
	}
	return nil
}

func (x *UpdateDetector) Stop(ctx context.Context) error {
	if err := x.indicator.Unregister(ctx); err != nil {
		return goerr.Wrap(err, "failed to unregister from the update indicator")
	}
	return nil
}

func (x *UpdateDetector) listUpdatedAt(ctx context.Context) (map[model.ItemID]time.Time, error) {
	entries, err := x.repo.ListEntries(ctx)
	if err != nil {
		return nil, storageErr(err, "failed to list items to record as known")
	}
	return updatedAtOf(entries), nil
}

func updatedAtOf(entries []*model.Entry) map[model.ItemID]time.Time {
	m := make(map[model.ItemID]time.Time, len(entries))
	for _, e := range entries {
		if !e.Item.ViewerReviewDone() {
			m[e.Item.ID] = e.Item.UpdatedAt
		}
	}
	return m
}
