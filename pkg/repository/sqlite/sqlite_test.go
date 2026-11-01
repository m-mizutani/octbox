package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/repository/sqlite"
)

var baseTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) (*sqlite.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "octbox.db")
	repo, err := sqlite.New(context.Background(), path)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = repo.Close() })
	return repo, path
}

func pr(id string, number int, updated time.Time) model.Item {
	return model.Item{
		ID:        model.ItemID(id),
		Kind:      model.ItemKindPullRequest,
		Repo:      model.RepoName{Owner: "example-org", Name: "api"},
		Number:    number,
		Title:     "Add retry " + id,
		URL:       fmt.Sprintf("https://github.com/example-org/api/pull/%d", number),
		Author:    "alice",
		State:     model.ItemStateOpen,
		CI:        model.CIStateSuccess,
		Review:    model.ReviewStateApproved,
		UpdatedAt: updated,
	}
}

func issue(id string, number int, updated time.Time) model.Item {
	return model.Item{
		ID:        model.ItemID(id),
		Kind:      model.ItemKindIssue,
		Repo:      model.RepoName{Owner: "octocat", Name: "hello-world"},
		Number:    number,
		Title:     "Crash " + id,
		URL:       fmt.Sprintf("https://github.com/octocat/hello-world/issues/%d", number),
		Author:    "carol",
		State:     model.ItemStateOpen,
		CI:        model.CIStateNone,
		Review:    model.ReviewStateNone,
		UpdatedAt: updated,
	}
}

func TestSaveItems(t *testing.T) {
	ctx := context.Background()

	t.Run("overwrite keeps read mark and categories", func(t *testing.T) {
		repo, _ := newRepo(t)
		item := pr("PR_1", 1, baseTime)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{item})).Required()
		gt.NoError(t, repo.AddCategory(ctx, model.CategoryReview, []model.ItemID{"PR_1"})).Required()
		gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_1": baseTime})).Required()

		item.Title = "Renamed"
		item.CI = model.CIStateFailure
		item.UpdatedAt = baseTime.Add(time.Hour)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{item})).Required()

		e, err := repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.Equal(t, e.Item, item)
		gt.A(t, e.Categories).Equal([]model.Category{model.CategoryReview})
		gt.Equal(t, e.ReadUpdatedAt, baseTime)
		gt.True(t, e.Unread())
	})

	t.Run("viewer review fields are saved and overwritten", func(t *testing.T) {
		repo, _ := newRepo(t)
		item := pr("PR_1", 1, baseTime)
		item.ViewerApprovedAt = baseTime.Add(-time.Hour)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{item})).Required()
		e, err := repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.Equal(t, e.Item, item)

		item.ViewerReviewRequested = true
		item.ViewerAddressedAt = baseTime.Add(-time.Minute)
		item.UpdatedAt = baseTime.Add(time.Minute)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{item})).Required()
		entries, err := repo.ListEntries(ctx)
		gt.NoError(t, err).Required()
		gt.A(t, entries).Length(1).Required()
		gt.Equal(t, entries[0].Item, item)

		item.ViewerApprovedAt = time.Time{}
		item.ViewerReviewRequested = false
		item.ViewerAddressedAt = time.Time{}
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{item})).Required()
		found, err := repo.FindItemsByRefs(ctx, []model.ItemRef{item.Ref()})
		gt.NoError(t, err).Required()
		gt.Equal(t, *found[item.Ref()], item)
	})

	t.Run("new ID for the same repository and number replaces the old row", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_old", 7, baseTime)})).Required()
		gt.NoError(t, repo.AddCategory(ctx, model.CategoryCreated, []model.ItemID{"PR_old"})).Required()
		gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_old": baseTime})).Required()

		gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_new", 7, baseTime)})).Required()

		_, err := repo.GetEntry(ctx, "PR_old")
		gt.Error(t, err).Is(interfaces.ErrItemNotFound)
		entries, err := repo.ListEntries(ctx)
		gt.NoError(t, err).Required()
		gt.A(t, entries).Length(1)
		gt.Equal(t, entries[0].Item.ID, model.ItemID("PR_new"))
		gt.A(t, entries[0].Categories).Length(0)
		gt.True(t, entries[0].ReadUpdatedAt.IsZero())
	})

	t.Run("invalid item saves nothing", func(t *testing.T) {
		repo, _ := newRepo(t)
		bad := pr("PR_bad", 2, baseTime)
		bad.Title = ""
		gt.Error(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime), bad}))
		entries, err := repo.ListEntries(ctx)
		gt.NoError(t, err).Required()
		gt.A(t, entries).Length(0)
	})

	t.Run("closed, merged, or archived item saves nothing", func(t *testing.T) {
		repo, _ := newRepo(t)
		closed := issue("I_1", 1, baseTime)
		closed.State = model.ItemStateClosed
		gt.Error(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime), closed}))

		merged := pr("PR_2", 2, baseTime)
		merged.State = model.ItemStateMerged
		gt.Error(t, repo.SaveItems(ctx, []model.Item{merged}))

		archived := pr("PR_3", 3, baseTime)
		archived.RepoArchived = true
		gt.Error(t, repo.SaveItems(ctx, []model.Item{archived}))

		entries, err := repo.ListEntries(ctx)
		gt.NoError(t, err).Required()
		gt.A(t, entries).Length(0)
	})
}

func TestCategories(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{
		pr("PR_1", 1, baseTime), pr("PR_2", 2, baseTime), issue("I_1", 1, baseTime),
	})).Required()

	gt.NoError(t, repo.AddCategory(ctx, model.CategoryReview, []model.ItemID{"PR_1", "PR_2"})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategoryReview, []model.ItemID{"PR_1"})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategoryCreated, []model.ItemID{"PR_1", "I_1"})).Required()

	e, err := repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.A(t, e.Categories).Equal([]model.Category{model.CategoryReview, model.CategoryCreated})

	gt.NoError(t, repo.ReplaceCategory(ctx, model.CategoryReview, []model.ItemID{"PR_2"})).Required()

	e, err = repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.A(t, e.Categories).Equal([]model.Category{model.CategoryCreated})
	e, err = repo.GetEntry(ctx, "PR_2")
	gt.NoError(t, err).Required()
	gt.A(t, e.Categories).Equal([]model.Category{model.CategoryReview})
	e, err = repo.GetEntry(ctx, "I_1")
	gt.NoError(t, err).Required()
	gt.A(t, e.Categories).Equal([]model.Category{model.CategoryCreated})

	gt.Error(t, repo.AddCategory(ctx, model.CategoryReview, []model.ItemID{"missing"}))
	gt.Error(t, repo.ReplaceCategory(ctx, model.CategoryReview, []model.ItemID{"missing"}))
}

func TestListAndGetEntries(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{
		pr("PR_b", 2, baseTime),
		pr("PR_a", 1, baseTime),
		issue("I_new", 3, baseTime.Add(time.Hour)),
	})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategorySubscribed, []model.ItemID{"PR_a"})).Required()
	gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_a": baseTime})).Required()

	entries, err := repo.ListEntries(ctx)
	gt.NoError(t, err).Required()
	gt.A(t, entries).Length(3).Required()
	gt.Equal(t, entries[0].Item.ID, model.ItemID("I_new"))
	gt.Equal(t, entries[1].Item.ID, model.ItemID("PR_a"))
	gt.Equal(t, entries[2].Item.ID, model.ItemID("PR_b"))
	gt.A(t, entries[1].Categories).Equal([]model.Category{model.CategorySubscribed})
	gt.Equal(t, entries[1].ReadUpdatedAt, baseTime)
	gt.Equal(t, entries[0].Item, issue("I_new", 3, baseTime.Add(time.Hour)))

	_, err = repo.GetEntry(ctx, "missing")
	gt.Error(t, err).Is(interfaces.ErrItemNotFound)
}

func TestFindAndListRefreshIDs(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	pending := pr("PR_pending", 1, baseTime)
	pending.CI = model.CIStatePending
	draft := pr("PR_draft", 2, baseTime)
	draft.IsDraft = true
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{pending, draft, issue("I_1", 1, baseTime)})).Required()

	found, err := repo.FindItemsByRefs(ctx, []model.ItemRef{
		pending.Ref(),
		{Repo: model.RepoName{Owner: "example-org", Name: "api"}, Number: 99},
	})
	gt.NoError(t, err).Required()
	gt.M(t, found).Length(1)
	gt.Equal(t, *found[pending.Ref()], pending)

	ids, err := repo.ListRefreshIDs(ctx, true)
	gt.NoError(t, err).Required()
	gt.A(t, ids).Equal([]model.ItemID{"PR_pending"})

	ids, err = repo.ListRefreshIDs(ctx, false)
	gt.NoError(t, err).Required()
	gt.A(t, ids).UnorderedEqual([]model.ItemID{"PR_pending", "PR_draft", "I_1"})
}

func TestReadMarks(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime)})).Required()

	gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_1": baseTime.Add(-time.Hour)})).Required()
	e, err := repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.Equal(t, e.ReadUpdatedAt, baseTime.Add(-time.Hour))

	gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_1": baseTime})).Required()
	e, err = repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.Equal(t, e.ReadUpdatedAt, baseTime)

	gt.NoError(t, repo.ClearRead(ctx, "PR_1")).Required()
	e, err = repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.True(t, e.ReadUpdatedAt.IsZero())

	gt.Error(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"missing": baseTime}))
}

func TestArchiveMarks(t *testing.T) {
	ctx := context.Background()

	t.Run("set, overwrite and clear", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime)})).Required()
		gt.NoError(t, repo.AddCategory(ctx, model.CategoryReview, []model.ItemID{"PR_1"})).Required()
		gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_1": baseTime.Add(-time.Hour)})).Required()

		gt.NoError(t, repo.SetArchived(ctx, "PR_1", baseTime.Add(-time.Minute))).Required()
		e, err := repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.Equal(t, e.ArchivedUpdatedAt, baseTime.Add(-time.Minute))
		gt.Equal(t, e.ReadUpdatedAt, baseTime.Add(-time.Hour))
		gt.A(t, e.Categories).Equal([]model.Category{model.CategoryReview})
		gt.Equal(t, e.Item, pr("PR_1", 1, baseTime))

		gt.NoError(t, repo.SetArchived(ctx, "PR_1", baseTime)).Required()
		entries, err := repo.ListEntries(ctx)
		gt.NoError(t, err).Required()
		gt.A(t, entries).Length(1)
		gt.Equal(t, entries[0].ArchivedUpdatedAt, baseTime)
		gt.Equal(t, entries[0].ReadUpdatedAt, baseTime.Add(-time.Hour))

		gt.NoError(t, repo.ClearArchived(ctx, "PR_1")).Required()
		e, err = repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.True(t, e.ArchivedUpdatedAt.IsZero())
		gt.Equal(t, e.ReadUpdatedAt, baseTime.Add(-time.Hour))

		// Clearing an item without a mark is not an error.
		gt.NoError(t, repo.ClearArchived(ctx, "PR_1"))
		gt.NoError(t, repo.ClearArchived(ctx, "missing"))
	})

	t.Run("unknown item fails", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.Error(t, repo.SetArchived(ctx, "missing", baseTime))
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("missing", 1, baseTime)})).Required()
		e, err := repo.GetEntry(ctx, "missing")
		gt.NoError(t, err).Required()
		gt.True(t, e.ArchivedUpdatedAt.IsZero())
	})

	t.Run("saving a later update keeps the mark and ends the archive", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime)})).Required()
		gt.NoError(t, repo.SetArchived(ctx, "PR_1", baseTime)).Required()

		// A save with the same updatedAt (for example, CI finished).
		same := pr("PR_1", 1, baseTime)
		same.CI = model.CIStateFailure
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{same})).Required()
		e, err := repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.True(t, e.Archived())

		gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime.Add(time.Minute))})).Required()
		e, err = repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.Equal(t, e.ArchivedUpdatedAt, baseTime)
		gt.False(t, e.Archived())
	})

	// Each way of deleting an item is checked on its own: the item is saved
	// again right after the deletion and must come back without a mark.
	deletions := []struct {
		name   string
		delete func(t *testing.T, repo *sqlite.Repository)
	}{
		{name: "DeleteItems", delete: func(t *testing.T, repo *sqlite.Repository) {
			gt.NoError(t, repo.DeleteItems(ctx, []model.ItemID{"PR_1"})).Required()
		}},
		{name: "PurgeUncategorized", delete: func(t *testing.T, repo *sqlite.Repository) {
			n, err := repo.PurgeUncategorized(ctx)
			gt.NoError(t, err).Required()
			gt.Equal(t, n, 1)
		}},
		{name: "ResetAll", delete: func(t *testing.T, repo *sqlite.Repository) {
			gt.NoError(t, repo.ResetAll(ctx)).Required()
		}},
	}
	for _, d := range deletions {
		t.Run(d.name+" removes the mark", func(t *testing.T) {
			repo, _ := newRepo(t)
			gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime)})).Required()
			gt.NoError(t, repo.SetArchived(ctx, "PR_1", baseTime)).Required()

			d.delete(t, repo)
			_, err := repo.GetEntry(ctx, "PR_1")
			gt.Error(t, err).Is(interfaces.ErrItemNotFound)

			gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime)})).Required()
			e, err := repo.GetEntry(ctx, "PR_1")
			gt.NoError(t, err).Required()
			gt.True(t, e.ArchivedUpdatedAt.IsZero())
		})
	}
}

func TestPurgeUncategorized(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{
		pr("PR_keep", 1, baseTime.Add(-365*24*time.Hour)),
		pr("PR_drop", 2, baseTime),
	})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategoryCreated, []model.ItemID{"PR_keep"})).Required()
	gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_drop": baseTime})).Required()

	n, err := repo.PurgeUncategorized(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, n, 1)

	entries, err := repo.ListEntries(ctx)
	gt.NoError(t, err).Required()
	gt.A(t, entries).Length(1)
	gt.Equal(t, entries[0].Item.ID, model.ItemID("PR_keep"))

	// The read mark went with the item: saving it again shows it unread.
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_drop", 2, baseTime)})).Required()
	e, err := repo.GetEntry(ctx, "PR_drop")
	gt.NoError(t, err).Required()
	gt.True(t, e.ReadUpdatedAt.IsZero())
}

func TestDeleteAndReset(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime), pr("PR_2", 2, baseTime)})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategoryCreated, []model.ItemID{"PR_1", "PR_2"})).Required()
	gt.NoError(t, repo.SetRead(ctx, map[model.ItemID]time.Time{"PR_1": baseTime})).Required()

	gt.NoError(t, repo.DeleteItems(ctx, []model.ItemID{"PR_1", "unknown"})).Required()
	_, err := repo.GetEntry(ctx, "PR_1")
	gt.Error(t, err).Is(interfaces.ErrItemNotFound)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{pr("PR_1", 1, baseTime)})).Required()
	e, err := repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.A(t, e.Categories).Length(0)
	gt.True(t, e.ReadUpdatedAt.IsZero())

	gt.NoError(t, repo.PutSyncState(ctx, &model.SyncState{Viewer: "alice", RateRemaining: 10, SearchCursor: baseTime})).Required()
	gt.NoError(t, repo.ResetAll(ctx)).Required()
	entries, err := repo.ListEntries(ctx)
	gt.NoError(t, err).Required()
	gt.A(t, entries).Length(0)
	st, err := repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, *st, *model.NewSyncState())
}

func TestSyncStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)

	st, err := repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, st.RateRemaining, -1)

	want := model.SyncState{
		Viewer:                   "alice",
		SearchCursor:             baseTime,
		NotificationCursor:       baseTime.Add(time.Second),
		NotificationLastModified: "Thu, 01 Oct 2026 12:00:00 GMT",
		LastFullSyncAt:           baseTime.Add(2 * time.Second),
		LastOpenRefreshAt:        baseTime.Add(3 * time.Second),
		LastSyncAt:               baseTime.Add(4 * time.Second),
		NextRunAt:                baseTime.Add(5 * time.Second),
		LeaseUntil:               baseTime.Add(7 * time.Second),
		ConsecutiveFailures:      3,
		LastError:                model.SyncErrorNetwork,
		RateRemaining:            4321,
		RateResetAt:              baseTime.Add(time.Hour),
	}
	gt.NoError(t, repo.PutSyncState(ctx, &want)).Required()
	st, err = repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, *st, want)
}

func TestClaimSync(t *testing.T) {
	ctx := context.Background()
	lease := 2 * time.Minute

	t.Run("lease", func(t *testing.T) {
		repo, path := newRepo(t)
		other, err := sqlite.New(ctx, path)
		gt.NoError(t, err).Required()
		t.Cleanup(func() { _ = other.Close() })

		st, ok, err := repo.ClaimSync(ctx, baseTime, lease, false)
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		gt.Equal(t, st.LeaseUntil, baseTime.Add(lease))

		_, ok, err = other.ClaimSync(ctx, baseTime.Add(time.Minute), lease, true)
		gt.NoError(t, err).Required()
		gt.False(t, ok)

		_, ok, err = other.ClaimSync(ctx, baseTime.Add(lease), lease, false)
		gt.NoError(t, err).Required()
		gt.True(t, ok)
	})

	t.Run("next run", func(t *testing.T) {
		repo, _ := newRepo(t)
		gt.NoError(t, repo.PutSyncState(ctx, &model.SyncState{
			RateRemaining: -1,
			NextRunAt:     baseTime.Add(time.Minute),
		})).Required()

		_, ok, err := repo.ClaimSync(ctx, baseTime.Add(time.Second), lease, false)
		gt.NoError(t, err).Required()
		gt.False(t, ok)

		_, ok, err = repo.ClaimSync(ctx, baseTime.Add(time.Second), lease, true)
		gt.NoError(t, err).Required()
		gt.True(t, ok)
	})

	t.Run("renew and release only while the lease is held", func(t *testing.T) {
		repo, path := newRepo(t)
		other, err := sqlite.New(ctx, path)
		gt.NoError(t, err).Required()
		t.Cleanup(func() { _ = other.Close() })

		st, ok, err := repo.ClaimSync(ctx, baseTime, lease, false)
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		held := st.LeaseUntil

		renewed := baseTime.Add(time.Minute + lease)
		ok, err = repo.RenewSync(ctx, held, renewed)
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		ok, err = repo.RenewSync(ctx, held, renewed.Add(time.Minute))
		gt.NoError(t, err).Required()
		gt.False(t, ok)

		// The lease expires and another process claims the sync.
		_, ok, err = other.ClaimSync(ctx, renewed, lease, true)
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		ok, err = repo.RenewSync(ctx, renewed, renewed.Add(lease))
		gt.NoError(t, err).Required()
		gt.False(t, ok)
		ok, err = repo.ReleaseSync(ctx, &model.SyncState{Viewer: "stale", RateRemaining: -1}, renewed)
		gt.NoError(t, err).Required()
		gt.False(t, ok)
		got, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, got.Viewer, "")
		gt.Equal(t, got.LeaseUntil, renewed.Add(lease))

		ok, err = other.ReleaseSync(ctx, &model.SyncState{Viewer: "alice", RateRemaining: -1}, renewed.Add(lease))
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		got, err = repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, got.Viewer, "alice")
		gt.True(t, got.LeaseUntil.IsZero())
	})

	t.Run("concurrent claims from two connections", func(t *testing.T) {
		_, path := newRepo(t)
		const n = 2
		repos := make([]*sqlite.Repository, n)
		for i := range repos {
			r, err := sqlite.New(ctx, path)
			gt.NoError(t, err).Required()
			t.Cleanup(func() { _ = r.Close() })
			repos[i] = r
		}

		for round := 0; round < 20; round++ {
			now := baseTime.Add(time.Duration(round) * time.Hour)
			var (
				wg      sync.WaitGroup
				mu      sync.Mutex
				claimed int
			)
			start := make(chan struct{})
			for _, r := range repos {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, ok, err := r.ClaimSync(ctx, now, lease, false)
					gt.NoError(t, err)
					if ok {
						mu.Lock()
						claimed++
						mu.Unlock()
					}
				}()
			}
			close(start)
			wg.Wait()
			gt.Equal(t, claimed, 1)
		}
	})
}
