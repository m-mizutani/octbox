package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/repository/sqlite"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/usecase/usecasetest"
)

// failingListRepo is the SQLite repository whose ListEntries fails while
// failures is positive, once per call.
type failingListRepo struct {
	*sqlite.Repository
	failures int
}

func (x *failingListRepo) ListEntries(ctx context.Context) ([]*model.Entry, error) {
	if x.failures > 0 {
		x.failures--
		return nil, errors.New("database is locked")
	}
	return x.Repository.ListEntries(ctx)
}

// saveRead stores items and marks them read at their updatedAt.
func saveRead(t *testing.T, repo *sqlite.Repository, items ...model.Item) {
	t.Helper()
	ctx := context.Background()
	gt.NoError(t, repo.SaveItems(ctx, items)).Required()
	marks := map[model.ItemID]time.Time{}
	for _, item := range items {
		marks[item.ID] = item.UpdatedAt
	}
	gt.NoError(t, repo.SetRead(ctx, marks)).Required()
}

func saveUnread(t *testing.T, repo *sqlite.Repository, items ...model.Item) {
	t.Helper()
	gt.NoError(t, repo.SaveItems(context.Background(), items)).Required()
}

func TestUpdateDetector(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*sqlite.Repository, *usecasetest.UpdateIndicator, *usecase.UpdateDetector) {
		repo, _ := newRepo(t)
		indicator := &usecasetest.UpdateIndicator{}
		return repo, indicator, usecase.NewUpdateDetector(repo, indicator)
	}

	t.Run("unread items present at start are not shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		saveRead(t, repo, issueItem("I_1", 2, t0))

		gt.NoError(t, detector.Start(ctx)).Required()
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.A(t, indicator.Calls).Equal([]string{"register"})
	})

	t.Run("a read item updated on GitHub is shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(5*time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.A(t, indicator.Calls).Equal([]string{"register", "show_update"})
	})

	t.Run("a new unread item is shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		gt.NoError(t, detector.Start(ctx)).Required()

		saveUnread(t, repo, issueItem("I_1", 2, t0))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("several updates are shown once", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0), issueItem("I_1", 2, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(time.Minute)), issueItem("I_1", 2, t0.Add(2*time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("an unread item fetched again with the same updatedAt is not shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		gt.NoError(t, detector.Start(ctx)).Required()
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)

		// Pull requests with pending CI are fetched again on every sync.
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("an item marked unread by the user is not shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		gt.NoError(t, repo.ClearRead(ctx, "PR_1")).Required()
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)
	})

	t.Run("an archived item updated on GitHub is shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, repo.SetArchived(ctx, "PR_1", t0)).Required()
		gt.NoError(t, detector.Start(ctx)).Required()

		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("archiving an unread item is not shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		gt.NoError(t, repo.SetArchived(ctx, "PR_1", t0)).Required()
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)
	})

	t.Run("an unread item taken out of the archive by the user is not shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, repo.SetArchived(ctx, "PR_1", t0)).Required()
		gt.NoError(t, detector.Start(ctx)).Required()
		gt.NoError(t, detector.Check(ctx)).Required()

		gt.NoError(t, repo.ClearArchived(ctx, "PR_1")).Required()
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)
	})

	t.Run("an update read before the check is not shown", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		saveRead(t, repo, prItem("PR_1", 1, t0.Add(time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)
	})

	t.Run("a deleted item is forgotten", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		gt.NoError(t, repo.DeleteItems(ctx, []model.ItemID{"PR_1"})).Required()
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)

		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("a second update is shown again", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(2*time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 2)
	})

	t.Run("an item approved by the viewer is not shown until a review is requested again", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		// Approving updates the pull request on GitHub.
		approved := prItem("PR_1", 1, t0.Add(time.Minute))
		approved.ViewerApprovedAt = t0.Add(time.Minute)
		saveUnread(t, repo, approved)
		gt.NoError(t, detector.Check(ctx)).Required()

		// A comment by the author after the approval, not addressed to the
		// viewer.
		approved.UpdatedAt = t0.Add(2 * time.Minute)
		saveUnread(t, repo, approved)
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)

		rerequested := approved
		rerequested.UpdatedAt = t0.Add(3 * time.Minute)
		rerequested.ViewerReviewRequested = true
		saveUnread(t, repo, rerequested)
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("an item approved by the viewer is shown when a comment is addressed to the viewer", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		approved := prItem("PR_1", 1, t0)
		approved.ViewerApprovedAt = t0
		saveRead(t, repo, approved)
		gt.NoError(t, detector.Start(ctx)).Required()

		addressed := approved
		addressed.UpdatedAt = t0.Add(time.Minute)
		addressed.ViewerAddressedAt = t0.Add(time.Minute)
		saveUnread(t, repo, addressed)
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("an unread item shown again with the same updatedAt counts as new", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		approved := prItem("PR_1", 1, t0)
		approved.ViewerApprovedAt = t0
		saveUnread(t, repo, approved)
		gt.NoError(t, detector.Start(ctx)).Required()

		dismissed := approved
		dismissed.ViewerApprovedAt = time.Time{}
		saveUnread(t, repo, dismissed)
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("a failed ShowUpdate is not retried", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		gt.NoError(t, detector.Start(ctx)).Required()

		indicator.ShowUpdateErr = errors.New("herdr is not running")
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		gt.Error(t, detector.Check(ctx))

		indicator.ShowUpdateErr = nil
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("a failed read at start makes the first check record only", func(t *testing.T) {
		sqliteRepo, _ := newRepo(t)
		repo := &failingListRepo{Repository: sqliteRepo, failures: 1}
		indicator := &usecasetest.UpdateIndicator{}
		detector := usecase.NewUpdateDetector(repo, indicator)
		saveUnread(t, sqliteRepo, prItem("PR_1", 1, t0))

		gt.Error(t, detector.Start(ctx))
		gt.A(t, indicator.Calls).Equal([]string{"register"})

		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 0)

		saveUnread(t, sqliteRepo, prItem("PR_1", 1, t0.Add(time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("a failed read at a check keeps the known items", func(t *testing.T) {
		sqliteRepo, _ := newRepo(t)
		repo := &failingListRepo{Repository: sqliteRepo}
		indicator := &usecasetest.UpdateIndicator{}
		detector := usecase.NewUpdateDetector(repo, indicator)
		saveRead(t, sqliteRepo, prItem("PR_1", 1, t0))
		gt.NoError(t, detector.Start(ctx)).Required()

		saveUnread(t, sqliteRepo, prItem("PR_1", 1, t0.Add(time.Minute)))
		repo.failures = 1
		gt.Error(t, detector.Check(ctx))
		gt.Equal(t, indicator.CallCount("show_update"), 0)

		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("a failed Register still records the known items", func(t *testing.T) {
		repo, indicator, detector := setup(t)
		saveRead(t, repo, prItem("PR_1", 1, t0))
		indicator.RegisterErr = errors.New("herdr is not running")
		gt.Error(t, detector.Start(ctx))

		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(time.Minute)))
		gt.NoError(t, detector.Check(ctx)).Required()
		gt.Equal(t, indicator.CallCount("show_update"), 1)
	})

	t.Run("Stop unregisters", func(t *testing.T) {
		_, indicator, detector := setup(t)
		gt.NoError(t, detector.Start(ctx)).Required()
		gt.NoError(t, detector.Stop(ctx)).Required()
		gt.A(t, indicator.Calls).Equal([]string{"register", "unregister"})

		indicator.UnregisterErr = errors.New("herdr is not running")
		gt.Error(t, detector.Stop(ctx))
	})
}
