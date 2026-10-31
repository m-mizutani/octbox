package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/repository/sqlite"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/usecase/usecasetest"
)

// failingSetRead is the SQLite repository with SetRead failing, as when the
// database is locked by another process beyond busy_timeout.
type failingSetRead struct {
	*sqlite.Repository
}

func (x *failingSetRead) SetRead(context.Context, map[model.ItemID]time.Time) error {
	return errors.New("database is locked")
}

func TestInbox(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)
	older := prItem("PR_1", 1, t0.Add(-time.Hour))
	newer := issueItem("I_1", 2, t0)
	orphan := prItem("PR_orphan", 3, t0.Add(time.Hour))
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{older, newer, orphan})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategoryCreated, []model.ItemID{"PR_1", "I_1"})).Required()

	browser := &usecasetest.Browser{}
	inbox := usecase.NewInbox(repo, browser)

	t.Run("List filters by category and terms, newest first", func(t *testing.T) {
		list, err := inbox.List(ctx, model.NewFilter(""))
		gt.NoError(t, err).Required()
		gt.A(t, list).Length(2).Required()
		gt.Equal(t, list[0].Item.ID, model.ItemID("I_1"))
		gt.Equal(t, list[1].Item.ID, model.ItemID("PR_1"))

		list, err = inbox.List(ctx, model.NewFilter("pr"))
		gt.NoError(t, err).Required()
		gt.A(t, list).Length(1)
		gt.Equal(t, list[0].Item.ID, model.ItemID("PR_1"))
	})

	t.Run("List leaves out pull requests approved by the viewer until a review is requested again or a comment is addressed to the viewer", func(t *testing.T) {
		repo, _ := newRepo(t)
		approved := prItem("PR_a", 10, t0)
		approved.ViewerApprovedAt = t0
		rerequested := prItem("PR_r", 11, t0.Add(time.Minute))
		rerequested.ViewerApprovedAt = t0
		rerequested.ViewerReviewRequested = true
		addressed := prItem("PR_m", 12, t0.Add(2*time.Minute))
		addressed.ViewerApprovedAt = t0
		addressed.ViewerAddressedAt = t0.Add(2 * time.Minute)
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{approved, rerequested, addressed})).Required()
		gt.NoError(t, repo.AddCategory(ctx, model.CategoryReview, []model.ItemID{"PR_a", "PR_r", "PR_m"})).Required()

		list, err := usecase.NewInbox(repo, &usecasetest.Browser{}).List(ctx, model.NewFilter(""))
		gt.NoError(t, err).Required()
		gt.A(t, list).Length(2).Required()
		gt.Equal(t, list[0].Item.ID, model.ItemID("PR_m"))
		gt.Equal(t, list[1].Item.ID, model.ItemID("PR_r"))
	})

	t.Run("Open marks read only when the browser opened", func(t *testing.T) {
		browser.Err = errors.New("no browser")
		openErr := inbox.Open(ctx, "PR_1")
		gt.True(t, goerr.HasTag(openErr, usecase.TagBrowserFailed))
		gt.False(t, goerr.HasTag(openErr, usecase.TagOpenedNotMarked))
		e, err := repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.True(t, e.Unread())

		browser.Err = nil
		gt.NoError(t, inbox.Open(ctx, "PR_1")).Required()
		gt.A(t, browser.Opened).Equal([]string{older.URL, older.URL})
		e, err = repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.False(t, e.Unread())
		gt.Equal(t, e.ReadUpdatedAt, older.UpdatedAt)
	})

	t.Run("ToggleRead", func(t *testing.T) {
		gt.NoError(t, inbox.ToggleRead(ctx, "PR_1")).Required()
		e, err := repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.True(t, e.Unread())

		gt.NoError(t, inbox.ToggleRead(ctx, "PR_1")).Required()
		e, err = repo.GetEntry(ctx, "PR_1")
		gt.NoError(t, err).Required()
		gt.False(t, e.Unread())
	})

	t.Run("MarkRead ignores unknown IDs", func(t *testing.T) {
		gt.NoError(t, inbox.MarkRead(ctx, []model.ItemID{"I_1", "PR_1", "missing"})).Required()
		list, err := inbox.List(ctx, model.NewFilter(""))
		gt.NoError(t, err).Required()
		for _, e := range list {
			gt.False(t, e.Unread())
		}
	})

	t.Run("Open reports a read mark failure after the browser opened", func(t *testing.T) {
		other, _ := newRepo(t)
		gt.NoError(t, other.SaveItems(ctx, []model.Item{older})).Required()
		b := &usecasetest.Browser{}
		x := usecase.NewInbox(&failingSetRead{Repository: other}, b)
		err := x.Open(ctx, "PR_1")
		gt.True(t, goerr.HasTag(err, usecase.TagOpenedNotMarked))
		gt.A(t, b.Opened).Length(1)
	})

	t.Run("SyncState", func(t *testing.T) {
		st, err := inbox.SyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.RateRemaining, -1)
	})
}
