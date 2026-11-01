package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/usecase/usecasetest"
)

func TestUnreadCountReporter(t *testing.T) {
	ctx := context.Background()

	t.Run("Report shows the number of unread items", func(t *testing.T) {
		repo, _ := newRepo(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0), issueItem("I_1", 2, t0))
		saveRead(t, repo, prItem("PR_2", 3, t0))
		display := &usecasetest.UnreadCountDisplay{}

		gt.NoError(t, usecase.NewUnreadCountReporter(repo, display, time.Hour).Report(ctx)).Required()
		gt.A(t, display.ShownCounts()).Equal([]int{2})
	})

	t.Run("Report leaves out items approved by the viewer until a review is requested again or a comment is addressed to the viewer", func(t *testing.T) {
		repo, _ := newRepo(t)
		approved := prItem("PR_1", 1, t0)
		approved.ViewerApprovedAt = t0
		rerequested := prItem("PR_2", 2, t0)
		rerequested.ViewerApprovedAt = t0
		rerequested.ViewerReviewRequested = true
		addressed := prItem("PR_3", 3, t0.Add(time.Minute))
		addressed.ViewerApprovedAt = t0
		addressed.ViewerAddressedAt = t0.Add(time.Minute)
		saveUnread(t, repo, approved, rerequested, addressed, issueItem("I_1", 4, t0))
		display := &usecasetest.UnreadCountDisplay{}

		gt.NoError(t, usecase.NewUnreadCountReporter(repo, display, time.Hour).Report(ctx)).Required()
		gt.A(t, display.ShownCounts()).Equal([]int{3})
	})

	t.Run("Report leaves out archived items until they are updated", func(t *testing.T) {
		repo, _ := newRepo(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0), issueItem("I_1", 2, t0))
		gt.NoError(t, repo.SetArchived(ctx, "PR_1", t0)).Required()
		display := &usecasetest.UnreadCountDisplay{}
		reporter := usecase.NewUnreadCountReporter(repo, display, time.Hour)

		gt.NoError(t, reporter.Report(ctx)).Required()
		saveUnread(t, repo, prItem("PR_1", 1, t0.Add(time.Minute)))
		gt.NoError(t, reporter.Report(ctx)).Required()
		gt.A(t, display.ShownCounts()).Equal([]int{1, 2})
	})

	t.Run("Report shows zero for an empty database", func(t *testing.T) {
		repo, _ := newRepo(t)
		display := &usecasetest.UnreadCountDisplay{}

		gt.NoError(t, usecase.NewUnreadCountReporter(repo, display, time.Hour).Report(ctx)).Required()
		gt.A(t, display.ShownCounts()).Equal([]int{0})
	})

	t.Run("Report returns a read failure without showing a count", func(t *testing.T) {
		repo, _ := newRepo(t)
		display := &usecasetest.UnreadCountDisplay{}

		err := usecase.NewUnreadCountReporter(&failingListRepo{Repository: repo, failures: 1}, display, time.Hour).Report(ctx)
		gt.Error(t, err)
		gt.A(t, display.ShownCounts()).Length(0)
	})

	t.Run("Report returns a display failure", func(t *testing.T) {
		repo, _ := newRepo(t)
		display := &usecasetest.UnreadCountDisplay{ShowErrs: []error{errors.New("herdr is gone")}}

		gt.Error(t, usecase.NewUnreadCountReporter(repo, display, time.Hour).Report(ctx))
	})

	t.Run("Run shows the count at every interval and follows read marks", func(t *testing.T) {
		repo, _ := newRepo(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0), prItem("PR_2", 2, t0))
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		display := &usecasetest.UnreadCountDisplay{}
		display.OnShow = func(int) {
			switch len(display.ShownCounts()) {
			case 1:
				// Read marks written by the inbox or another process.
				saveRead(t, repo, prItem("PR_1", 1, t0))
			case 3:
				cancel()
			}
		}

		usecase.NewUnreadCountReporter(repo, display, time.Millisecond).Run(ctx)
		gt.A(t, display.ShownCounts()).Equal([]int{2, 1, 1})
	})

	t.Run("Run keeps going after a failure", func(t *testing.T) {
		repo, _ := newRepo(t)
		saveUnread(t, repo, prItem("PR_1", 1, t0))
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		display := &usecasetest.UnreadCountDisplay{
			ShowErrs: []error{errors.New("herdr is gone"), errors.New("herdr is gone")},
		}
		display.OnShow = func(int) {
			if len(display.ShownCounts()) == 3 {
				cancel()
			}
		}

		usecase.NewUnreadCountReporter(repo, display, time.Millisecond).Run(ctx)
		gt.A(t, display.ShownCounts()).Equal([]int{1, 1, 1})
	})

	t.Run("Run returns at once when ctx is done", func(t *testing.T) {
		repo, _ := newRepo(t)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		display := &usecasetest.UnreadCountDisplay{}

		usecase.NewUnreadCountReporter(repo, display, time.Hour).Run(ctx)
		gt.True(t, len(display.ShownCounts()) <= 1)
	})

	t.Run("Stop removes the count", func(t *testing.T) {
		repo, _ := newRepo(t)
		display := &usecasetest.UnreadCountDisplay{}

		gt.NoError(t, usecase.NewUnreadCountReporter(repo, display, time.Hour).Stop(ctx)).Required()
		gt.Equal(t, display.Removed, 1)
	})
}
