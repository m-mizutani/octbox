package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/repository/sqlite"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/usecase/usecasetest"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newRepo(t *testing.T) (*sqlite.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "octbox.db")
	repo, err := sqlite.New(context.Background(), path)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = repo.Close() })
	return repo, path
}

func prItem(id string, number int, updated time.Time) model.Item {
	return model.Item{
		ID:        model.ItemID(id),
		Kind:      model.ItemKindPullRequest,
		Repo:      model.RepoName{Owner: "example-org", Name: "api"},
		Number:    number,
		Title:     "Change " + id,
		URL:       fmt.Sprintf("https://github.com/example-org/api/pull/%d", number),
		Author:    "bob",
		State:     model.ItemStateOpen,
		CI:        model.CIStateSuccess,
		Review:    model.ReviewStateRequired,
		UpdatedAt: updated,
	}
}

func issueItem(id string, number int, updated time.Time) model.Item {
	return model.Item{
		ID:        model.ItemID(id),
		Kind:      model.ItemKindIssue,
		Repo:      model.RepoName{Owner: "octocat", Name: "hello-world"},
		Number:    number,
		Title:     "Bug " + id,
		URL:       fmt.Sprintf("https://github.com/octocat/hello-world/issues/%d", number),
		Author:    "carol",
		State:     model.ItemStateOpen,
		CI:        model.CIStateNone,
		Review:    model.ReviewStateNone,
		UpdatedAt: updated,
	}
}

// searchBy answers SearchItems with the items registered for the first
// search qualifier of the query (e.g. "author:alice").
type searchBy struct {
	mu    sync.Mutex
	items map[string][]model.Item
	rate  model.RateLimit
}

func newSearchBy() *searchBy {
	return &searchBy{items: map[string][]model.Item{}, rate: model.RateLimit{Remaining: 4000, ResetAt: t0.Add(time.Hour)}}
}

func (s *searchBy) set(qualifier string, items ...model.Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[qualifier] = items
}

func (s *searchBy) fn(_ context.Context, query, _ string) (*model.SearchPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for qualifier, items := range s.items {
		if strings.Contains(query, qualifier+" ") {
			return &model.SearchPage{Items: items, RateLimit: s.rate}, nil
		}
	}
	return &model.SearchPage{RateLimit: s.rate}, nil
}

func entries(t *testing.T, repo *sqlite.Repository) map[model.ItemID]*model.Entry {
	t.Helper()
	list, err := repo.ListEntries(context.Background())
	gt.NoError(t, err).Required()
	m := map[model.ItemID]*model.Entry{}
	for _, e := range list {
		m[e.Item.ID] = e
	}
	return m
}

func setup(t *testing.T) (*usecasetest.GitHubClient, *sqlite.Repository, *clock, *usecase.Syncer, *searchBy) {
	t.Helper()
	repo, _ := newRepo(t)
	clk := &clock{now: t0}
	search := newSearchBy()
	client := &usecasetest.GitHubClient{SearchItemsFunc: search.fn}
	syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(clk.Now))
	return client, repo, clk, syncer, search
}

func TestSyncFirst(t *testing.T) {
	ctx := context.Background()
	client, repo, _, syncer, search := setup(t)
	search.set("review-requested:alice", prItem("PR_1", 1, t0.Add(-time.Hour)))
	search.set("author:alice", issueItem("I_1", 1, t0.Add(-2*time.Hour)))
	search.set("assignee:alice", issueItem("I_2", 2, t0.Add(-3*time.Hour)))

	report, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	gt.True(t, report.Full)
	gt.Equal(t, report.Saved, 3)

	gt.A(t, client.SearchCalls).Length(5).Required()
	gt.Equal(t, client.SearchCalls[0].Query, "is:pr review-requested:alice archived:false is:open sort:updated-desc")
	gt.Equal(t, client.SearchCalls[1].Query, "is:pr reviewed-by:alice archived:false is:open sort:updated-desc")
	gt.Equal(t, client.SearchCalls[2].Query, "author:alice archived:false is:open sort:updated-desc")
	gt.Equal(t, client.SearchCalls[3].Query, "commenter:alice archived:false is:open sort:updated-desc")
	gt.Equal(t, client.SearchCalls[4].Query, "assignee:alice archived:false is:open sort:updated-desc")
	for _, c := range client.SearchCalls {
		gt.S(t, c.Query).NotContains("updated:")
	}

	gt.A(t, client.NotificationCalls).Length(1).Required()
	gt.Equal(t, client.NotificationCalls[0].Since, t0.Add(-30*24*time.Hour))
	gt.Equal(t, client.NotificationCalls[0].LastModified, "")

	got := entries(t, repo)
	gt.M(t, got).Length(3)
	for _, e := range got {
		gt.False(t, e.Unread())
	}
	gt.A(t, got["PR_1"].Categories).Equal([]model.Category{model.CategoryReview})
	gt.A(t, got["I_2"].Categories).Equal([]model.Category{model.CategoryAssigned})

	st, err := repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, st.Viewer, "alice")
	gt.Equal(t, st.SearchCursor, t0)
	gt.Equal(t, st.NotificationCursor, t0)
	gt.Equal(t, st.LastFullSyncAt, t0)
	gt.Equal(t, st.LastOpenRefreshAt, t0)
	gt.Equal(t, st.NextRunAt, t0.Add(time.Minute))
	gt.Equal(t, st.RateRemaining, 4000)
	gt.True(t, st.LeaseUntil.IsZero())
}

func TestSyncIncremental(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	search.set("author:alice", issueItem("I_old", 1, t0.Add(-time.Hour)), issueItem("I_upd", 2, t0.Add(-time.Hour)))
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	clk.Advance(61 * time.Second)
	client.Reset()
	search.set("author:alice", issueItem("I_upd", 2, t0.Add(30*time.Second)), issueItem("I_new", 3, t0.Add(40*time.Second)))
	report, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	gt.False(t, report.Full)

	gt.A(t, client.SearchCalls).Length(5).Required()
	for _, c := range client.SearchCalls {
		gt.S(t, c.Query).Contains("updated:>=2026-10-01T11:50:00Z")
		gt.S(t, c.Query).NotContains("is:open")
		gt.S(t, c.Query).NotContains("archived:false")
	}

	got := entries(t, repo)
	gt.False(t, got["I_old"].Unread())
	gt.True(t, got["I_upd"].Unread())
	gt.True(t, got["I_new"].Unread())
	gt.A(t, got["I_new"].Categories).Equal([]model.Category{model.CategoryCreated})
}

func TestSyncNotifications(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	local := prItem("PR_local", 1, t0.Add(-time.Hour))
	stale := prItem("PR_stale", 2, t0.Add(-time.Hour))
	search.set("author:alice", local, stale)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	t.Run("304 resolves nothing", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		client.Reset()
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.A(t, client.NotificationCalls).Length(1)
		gt.Equal(t, client.NotificationCalls[0].Since, t0.Add(-10*time.Minute))
		gt.A(t, client.ResolveCalls).Length(0)
	})

	t.Run("new notifications", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		client.Reset()
		search.set("author:alice")

		threads := []model.NotificationThread{
			{Ref: local.Ref(), UpdatedAt: local.UpdatedAt},
			{Ref: stale.Ref(), UpdatedAt: t0},
		}
		for i := 0; i < 59; i++ {
			threads = append(threads, model.NotificationThread{
				Ref: model.ItemRef{Repo: model.RepoName{Owner: "octocat", Name: "hello-world"}, Number: 100 + i}, UpdatedAt: t0,
			})
		}
		client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
			return &model.NotificationPage{Threads: threads, LastModified: "Thu, 01 Oct 2026 12:02:00 GMT", PollInterval: 120 * time.Second}, nil
		}
		client.ResolveItemsFunc = func(_ context.Context, refs []model.ItemRef) (*model.ItemsResult, error) {
			var items []model.Item
			for _, ref := range refs {
				if ref == stale.Ref() {
					s := stale
					s.UpdatedAt = t0
					items = append(items, s)
					continue
				}
				items = append(items, issueItem(fmt.Sprintf("I_%d", ref.Number), ref.Number, t0))
			}
			return &model.ItemsResult{Items: items, RateLimit: model.RateLimit{Remaining: -1}}, nil
		}

		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()

		gt.A(t, client.ResolveCalls).Length(2).Required()
		gt.A(t, client.ResolveCalls[0]).Length(50)
		gt.A(t, client.ResolveCalls[1]).Length(10)
		gt.Equal(t, client.ResolveCalls[0][0], stale.Ref())
		for _, call := range client.ResolveCalls {
			for _, ref := range call {
				gt.NotEqual(t, ref, local.Ref())
			}
		}

		got := entries(t, repo)
		gt.M(t, got).Length(61)
		for _, e := range got {
			gt.A(t, e.Categories).Has(model.CategorySubscribed)
		}
		gt.True(t, got["PR_stale"].Unread())

		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.NotificationLastModified, "Thu, 01 Oct 2026 12:02:00 GMT")
		gt.Equal(t, st.NextRunAt, clk.Now().Add(120*time.Second))
	})
}

func TestSyncForcedEarlySkipsNotifications(t *testing.T) {
	ctx := context.Background()
	client, _, clk, syncer, _ := setup(t)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	clk.Advance(20 * time.Second)
	client.Reset()
	report, err := syncer.RunOnce(ctx, true)
	gt.NoError(t, err).Required()
	gt.False(t, report.Skipped)
	gt.A(t, client.SearchCalls).Length(5)
	gt.A(t, client.NotificationCalls).Length(0)
}

func TestSyncRefresh(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)

	var prs []model.Item
	for i := 1; i <= 150; i++ {
		p := prItem(fmt.Sprintf("PR_%03d", i), i, t0.Add(-time.Hour))
		if i <= 3 {
			p.CI = model.CIStatePending
		}
		prs = append(prs, p)
	}
	search.set("author:alice", prs...)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	gt.A(t, client.GetItemsCalls).Length(0)

	client.GetItemsFunc = func(_ context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
		result := &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}
		for _, id := range ids {
			if id == "PR_002" {
				result.MissingIDs = append(result.MissingIDs, id)
				continue
			}
			var n int
			_, _ = fmt.Sscanf(string(id), "PR_%d", &n)
			p := prItem(string(id), n, t0.Add(-time.Hour))
			p.CI = model.CIStateFailure
			result.Items = append(result.Items, p)
		}
		return result, nil
	}

	t.Run("pending only within 10 minutes", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		client.Reset()
		p3 := prs[2]
		p3.UpdatedAt = t0
		search.set("author:alice", p3)

		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.A(t, client.GetItemsCalls).Length(1).Required()
		gt.A(t, client.GetItemsCalls[0]).Equal([]model.ItemID{"PR_001", "PR_002"})

		got := entries(t, repo)
		_, ok := got["PR_002"]
		gt.False(t, ok)
		gt.Equal(t, got["PR_001"].Item.CI, model.CIStateFailure)
	})

	t.Run("every pull request after 10 minutes", func(t *testing.T) {
		clk.Advance(10 * time.Minute)
		client.Reset()
		search.set("author:alice")

		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.A(t, client.GetItemsCalls).Length(3).Required()
		gt.A(t, client.GetItemsCalls[0]).Length(50)
		gt.A(t, client.GetItemsCalls[1]).Length(50)
		gt.A(t, client.GetItemsCalls[2]).Length(49)

		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.LastOpenRefreshAt, clk.Now())
	})
}

func TestSyncArchivedAndDeletedRepositories(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	keep := issueItem("I_keep", 1, t0.Add(-time.Hour))
	deleted := issueItem("I_deleted", 2, t0.Add(-time.Hour))
	archivedPR := prItem("PR_archived", 3, t0.Add(-time.Hour))
	closedThenArchived := prItem("PR_closed", 4, t0.Add(-time.Hour))
	search.set("author:alice", keep, deleted, archivedPR, closedThenArchived)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	client.GetItemsFunc = func(_ context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
		result := &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}
		for _, id := range ids {
			switch id {
			case keep.ID:
				result.Items = append(result.Items, keep)
			case archivedPR.ID:
				a := archivedPR
				a.RepoArchived = true
				result.Items = append(result.Items, a)
			case deleted.ID:
				result.MissingIDs = append(result.MissingIDs, id)
			}
		}
		return result, nil
	}

	t.Run("a scheduled sync within 10 minutes refreshes no issue", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		client.Reset()
		search.set("author:alice")
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.A(t, client.GetItemsCalls).Length(0)
		gt.M(t, entries(t, repo)).Length(4)
	})

	t.Run("a forced sync refreshes every stored item and deletes archived and deleted ones", func(t *testing.T) {
		clk.Advance(10 * time.Second)
		client.Reset()
		c := closedThenArchived
		c.State = model.ItemStateClosed
		c.RepoArchived = true
		c.UpdatedAt = t0
		search.set("author:alice", c)

		_, err := syncer.RunOnce(ctx, true)
		gt.NoError(t, err).Required()

		var refreshed []model.ItemID
		for _, call := range client.GetItemsCalls {
			refreshed = append(refreshed, call...)
		}
		gt.A(t, refreshed).UnorderedEqual([]model.ItemID{keep.ID, deleted.ID, archivedPR.ID})

		got := entries(t, repo)
		gt.M(t, got).Length(1)
		gt.NotNil(t, got["I_keep"])

		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.LastOpenRefreshAt, clk.Now())
	})

	t.Run("an open item of an archived repository found by search is deleted", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		client.Reset()
		k := keep
		k.RepoArchived = true
		k.UpdatedAt = t0.Add(3 * time.Minute)
		search.set("author:alice", k)

		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.M(t, entries(t, repo)).Length(0)
	})
}

func TestSyncFull(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	reviewed := prItem("PR_review", 1, t0.Add(-time.Hour))
	both := prItem("PR_both", 2, t0.Add(-time.Hour))
	assigned := issueItem("I_assigned", 3, t0.Add(-time.Hour))
	subscribed := issueItem("I_sub", 4, t0.Add(-time.Hour))
	search.set("review-requested:alice", reviewed, both)
	search.set("author:alice", both)
	search.set("assignee:alice", assigned)
	client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
		return &model.NotificationPage{Threads: []model.NotificationThread{{Ref: subscribed.Ref(), UpdatedAt: subscribed.UpdatedAt}}}, nil
	}
	client.ResolveItemsFunc = func(context.Context, []model.ItemRef) (*model.ItemsResult, error) {
		return &model.ItemsResult{Items: []model.Item{subscribed}, RateLimit: model.RateLimit{Remaining: -1}}, nil
	}
	client.GetItemsFunc = func(_ context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
		return &model.ItemsResult{Items: []model.Item{both}, RateLimit: model.RateLimit{Remaining: -1}}, nil
	}
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	gt.M(t, entries(t, repo)).Length(4)
	client.ListNotificationsFunc = nil

	t.Run("incremental before 6 hours", func(t *testing.T) {
		clk.Advance(5 * time.Hour)
		client.Reset()
		search.set("review-requested:alice")
		search.set("assignee:alice")
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.False(t, report.Full)
		gt.M(t, entries(t, repo)).Length(4)
	})

	t.Run("full after 6 hours", func(t *testing.T) {
		clk.Advance(time.Hour)
		client.Reset()
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.True(t, report.Full)
		for _, c := range client.SearchCalls {
			gt.S(t, c.Query).Contains("is:open")
		}

		got := entries(t, repo)
		gt.M(t, got).Length(2)
		_, ok := got["PR_review"]
		gt.False(t, ok)
		_, ok = got["I_assigned"]
		gt.False(t, ok)
		gt.A(t, got["PR_both"].Categories).Equal([]model.Category{model.CategoryCreated})
		gt.A(t, got["I_sub"].Categories).Equal([]model.Category{model.CategorySubscribed})
	})
}

func TestSyncClosedAndMerged(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	open := issueItem("I_open", 1, t0.Add(-time.Hour))
	toClose := issueItem("I_close", 2, t0.Add(-time.Hour))
	toMerge := prItem("PR_merge", 3, t0.Add(-time.Hour))
	notified := prItem("PR_notified", 4, t0.Add(-time.Hour))
	refreshed := prItem("PR_refreshed", 5, t0.Add(-time.Hour))
	search.set("author:alice", open, toClose, toMerge, notified, refreshed)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	clk.Advance(11 * time.Minute)
	client.Reset()
	closed := toClose
	closed.State = model.ItemStateClosed
	closed.UpdatedAt = t0
	merged := toMerge
	merged.State = model.ItemStateMerged
	merged.UpdatedAt = t0
	unknownClosed := issueItem("I_unknown", 9, t0)
	unknownClosed.State = model.ItemStateClosed
	search.set("author:alice", closed, merged, unknownClosed)

	client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
		return &model.NotificationPage{Threads: []model.NotificationThread{{Ref: notified.Ref(), UpdatedAt: t0}}}, nil
	}
	client.ResolveItemsFunc = func(context.Context, []model.ItemRef) (*model.ItemsResult, error) {
		n := notified
		n.State = model.ItemStateClosed
		return &model.ItemsResult{Items: []model.Item{n}, RateLimit: model.RateLimit{Remaining: -1}}, nil
	}
	client.GetItemsFunc = func(_ context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
		r := refreshed
		r.State = model.ItemStateMerged
		return &model.ItemsResult{Items: []model.Item{r}, RateLimit: model.RateLimit{Remaining: -1}}, nil
	}

	_, err = syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	got := entries(t, repo)
	gt.M(t, got).Length(1)
	gt.NotNil(t, got["I_open"])

	t.Run("reopened item is saved unread", func(t *testing.T) {
		clk.Advance(61 * time.Second)
		client.Reset()
		client.ListNotificationsFunc = nil
		client.GetItemsFunc = nil
		reopened := toClose
		reopened.UpdatedAt = t0.Add(time.Minute)
		search.set("author:alice", reopened)
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		got := entries(t, repo)
		gt.True(t, got["I_close"].Unread())
	})
}

func TestSyncPaging(t *testing.T) {
	ctx := context.Background()
	client, _, _, syncer, _ := setup(t)
	client.SearchItemsFunc = func(_ context.Context, query, after string) (*model.SearchPage, error) {
		if !strings.Contains(query, "review-requested:") {
			return &model.SearchPage{RateLimit: model.RateLimit{Remaining: -1}}, nil
		}
		n := 0
		if after != "" {
			_, _ = fmt.Sscanf(after, "c%d", &n)
		}
		return &model.SearchPage{
			Items:       []model.Item{prItem(fmt.Sprintf("PR_%d", n), n+1, t0)},
			HasNextPage: true,
			EndCursor:   fmt.Sprintf("c%d", n+1),
			RateLimit:   model.RateLimit{Remaining: -1},
		}, nil
	}
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	var reviewCalls []usecasetest.SearchCall
	for _, c := range client.SearchCalls {
		if strings.Contains(c.Query, "review-requested:") {
			reviewCalls = append(reviewCalls, c)
		}
	}
	gt.A(t, reviewCalls).Length(40).Required()
	gt.Equal(t, reviewCalls[0].After, "")
	gt.Equal(t, reviewCalls[1].After, "c1")
	gt.Equal(t, reviewCalls[39].After, "c39")
}

func TestSyncRetriesGatewayErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("502 then success", func(t *testing.T) {
		client, repo, clk, _, search := setup(t)
		var slept []time.Duration
		syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(clk.Now),
			usecase.WithSleep(func(_ context.Context, d time.Duration) error {
				slept = append(slept, d)
				return nil
			}))
		failures := 2
		client.SearchItemsFunc = func(ctx context.Context, query, after string) (*model.SearchPage, error) {
			if failures > 0 {
				failures--
				return nil, &interfaces.APIError{StatusCode: 502}
			}
			return search.fn(ctx, query, after)
		}
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.Equal(t, report.State.ConsecutiveFailures, 0)
		gt.A(t, slept).Equal([]time.Duration{2 * time.Second, 5 * time.Second})
	})

	t.Run("no retry for other errors", func(t *testing.T) {
		client, repo, clk, _, _ := setup(t)
		var slept []time.Duration
		syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(clk.Now),
			usecase.WithSleep(func(_ context.Context, d time.Duration) error {
				slept = append(slept, d)
				return nil
			}))
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
			return nil, &interfaces.APIError{StatusCode: 500}
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		gt.A(t, slept).Length(0)
		gt.A(t, client.SearchCalls).Length(1)
	})
}

func TestSyncFullFailureFallsBack(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	merged := prItem("PR_m", 9, t0)
	merged.State = model.ItemStateOpen
	search.set("author:alice", merged)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	_, ok := entries(t, repo)["PR_m"]
	gt.True(t, ok).Required()

	// Six hours later every full search fails, and the pull request has
	// been merged; the regular search does not return it, so only the
	// pull request refresh can find out.
	clk.Advance(6 * time.Hour)
	client.Reset()
	search.set("author:alice")
	client.SearchItemsFunc = func(ctx context.Context, query, after string) (*model.SearchPage, error) {
		if strings.Contains(query, "is:open") {
			return nil, &interfaces.APIError{StatusCode: 500}
		}
		return search.fn(ctx, query, after)
	}
	gone := merged
	gone.State = model.ItemStateMerged
	client.GetItemsFunc = func(_ context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
		return &model.ItemsResult{Items: []model.Item{gone}, RateLimit: model.RateLimit{Remaining: -1}}, nil
	}

	report, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	gt.False(t, report.Full)
	gt.Equal(t, report.State.ConsecutiveFailures, 0)
	gt.Equal(t, report.State.SearchCursor, t0.Add(6*time.Hour))
	gt.Equal(t, report.State.LastFullSyncAt, t0)
	_, ok = entries(t, repo)["PR_m"]
	gt.False(t, ok)

	// The full sync is not tried again within 15 minutes.
	clk.Advance(time.Minute)
	client.Reset()
	_, err = syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	for _, c := range client.SearchCalls {
		gt.S(t, c.Query).NotContains("is:open")
	}

	// After 15 minutes it is.
	clk.Advance(15 * time.Minute)
	client.Reset()
	_, _ = syncer.RunOnce(ctx, false)
	gt.S(t, client.SearchCalls[0].Query).Contains("is:open")
}

func TestSyncLease(t *testing.T) {
	ctx := context.Background()
	repo, path := newRepo(t)
	other, err := sqlite.New(ctx, path)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = other.Close() })

	_, ok, err := other.ClaimSync(ctx, t0, 2*time.Minute, false)
	gt.NoError(t, err).Required()
	gt.True(t, ok)

	client := &usecasetest.GitHubClient{}
	clk := &clock{now: t0.Add(time.Minute)}
	syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(clk.Now))
	report, err := syncer.RunOnce(ctx, true)
	gt.NoError(t, err).Required()
	gt.True(t, report.Skipped)
	gt.Equal(t, client.ViewerCalls, 0)
	gt.A(t, client.SearchCalls).Length(0)

	t.Run("before the next run", func(t *testing.T) {
		clk.Advance(2 * time.Minute)
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		clk.Advance(30 * time.Second)
		client.Reset()
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.True(t, report.Skipped)
		gt.A(t, client.SearchCalls).Length(0)
	})
}

func TestSyncRateLimit(t *testing.T) {
	ctx := context.Background()

	t.Run("low remaining points", func(t *testing.T) {
		_, repo, _, syncer, search := setup(t)
		reset := t0.Add(40 * time.Minute)
		search.rate = model.RateLimit{Remaining: 150, ResetAt: reset}
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.Equal(t, report.State.NextRunAt, reset)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.RateRemaining, 150)
	})

	t.Run("rate limited error", func(t *testing.T) {
		client, repo, _, syncer, _ := setup(t)
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
			return nil, &interfaces.RateLimitedError{RetryAfter: 30 * time.Second}
		}
		_, err := syncer.RunOnce(ctx, false)
		var rle *interfaces.RateLimitedError
		gt.True(t, errors.As(err, &rle))
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.LastError, model.SyncErrorRateLimited)
		gt.Equal(t, st.NextRunAt, t0.Add(30*time.Second))
	})
}

func TestSyncForcedRunsWithoutWaiting(t *testing.T) {
	ctx := context.Background()

	t.Run("right after a sync", func(t *testing.T) {
		client, _, clk, syncer, _ := setup(t)
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		clk.Advance(time.Second)
		client.Reset()
		report, err := syncer.RunOnce(ctx, true)
		gt.NoError(t, err).Required()
		gt.False(t, report.Skipped)
		gt.True(t, len(client.SearchCalls) > 0)
	})

	t.Run("during the backoff after a failure", func(t *testing.T) {
		client, _, clk, syncer, _ := setup(t)
		search := client.SearchItemsFunc
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
			return nil, &interfaces.APIError{StatusCode: 500}
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		clk.Advance(time.Second)
		client.Reset()
		client.SearchItemsFunc = search
		report, err := syncer.RunOnce(ctx, true)
		gt.NoError(t, err).Required()
		gt.False(t, report.Skipped)
		gt.Equal(t, report.State.ConsecutiveFailures, 0)
	})
}

func TestSyncFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("backoff", func(t *testing.T) {
		client, repo, clk, syncer, _ := setup(t)
		netErr := &url.Error{Op: "Post", URL: "https://api.github.com/graphql", Err: errors.New("connection refused")}
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) { return nil, netErr }

		want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
			15 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute}
		for i, w := range want {
			_, err := syncer.RunOnce(ctx, false)
			gt.Error(t, err)
			st, err := repo.GetSyncState(ctx)
			gt.NoError(t, err).Required()
			gt.Equal(t, st.LastError, model.SyncErrorNetwork)
			gt.Equal(t, st.ConsecutiveFailures, i+1)
			gt.Equal(t, st.NextRunAt, clk.Now().Add(w))
			clk.Advance(w)
		}

		client.SearchItemsFunc = nil
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.ConsecutiveFailures, 0)
		gt.Equal(t, st.LastError, model.SyncErrorNone)
	})

	t.Run("failure during search keeps saved items and the cursor", func(t *testing.T) {
		client, repo, _, syncer, search := setup(t)
		search.set("review-requested:alice", prItem("PR_1", 1, t0))
		client.SearchItemsFunc = func(ctx context.Context, query, after string) (*model.SearchPage, error) {
			if strings.Contains(query, "author:") {
				return nil, &interfaces.APIError{StatusCode: 500}
			}
			return search.fn(ctx, query, after)
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		gt.M(t, entries(t, repo)).Length(1)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.True(t, st.SearchCursor.IsZero())
		gt.Equal(t, st.LastError, model.SyncErrorAPI)
	})

	t.Run("failure during notifications keeps the search cursor", func(t *testing.T) {
		client, repo, clk, syncer, _ := setup(t)
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()

		clk.Advance(61 * time.Second)
		client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
			return nil, &interfaces.APIError{StatusCode: 500}
		}
		_, err = syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.SearchCursor, clk.Now())
		gt.Equal(t, st.NotificationCursor, t0)
	})

	t.Run("a failed first sync starts over and marks every stored item read", func(t *testing.T) {
		client, repo, clk, syncer, search := setup(t)
		search.set("author:alice", issueItem("I_1", 1, t0.Add(-time.Hour)))
		subscribed := issueItem("I_sub", 2, t0.Add(-time.Hour))
		client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
			return &model.NotificationPage{Threads: []model.NotificationThread{{Ref: subscribed.Ref(), UpdatedAt: subscribed.UpdatedAt}}}, nil
		}
		client.ResolveItemsFunc = func(context.Context, []model.ItemRef) (*model.ItemsResult, error) {
			return &model.ItemsResult{Items: []model.Item{subscribed}, RateLimit: model.RateLimit{Remaining: -1}}, nil
		}
		client.GetItemsFunc = func(context.Context, []model.ItemID) (*model.ItemsResult, error) {
			return nil, &interfaces.APIError{StatusCode: 500}
		}
		// The first sync fails at the refresh step, after saving I_1 and
		// I_sub. The retry does not fetch I_sub again (its notification is
		// not newer than the stored item), so only a baseline over every
		// stored item marks it read.
		search.set("author:alice", issueItem("I_1", 1, t0.Add(-time.Hour)), prItem("PR_1", 3, t0.Add(-time.Hour)))
		gt.NoError(t, repo.SaveItems(ctx, []model.Item{prItem("PR_old", 4, t0.Add(-2*time.Hour))})).Required()
		// PR_old is not found by search, so the refresh step fetches it.
		gt.NoError(t, repo.AddCategory(ctx, model.CategorySubscribed, []model.ItemID{"PR_old"})).Required()
		_, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.True(t, st.SearchCursor.IsZero())
		gt.True(t, st.NotificationCursor.IsZero())

		clk.Advance(2 * time.Minute)
		client.GetItemsFunc = nil
		client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
			return &model.NotificationPage{Threads: []model.NotificationThread{{Ref: subscribed.Ref(), UpdatedAt: subscribed.UpdatedAt}}}, nil
		}
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.True(t, report.Full)
		for id, e := range entries(t, repo) {
			gt.False(t, e.Unread()).Describef("%s should be read", id)
		}
	})

	t.Run("unknown error", func(t *testing.T) {
		client, repo, _, syncer, _ := setup(t)
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
			return nil, errors.New("x")
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.LastError, model.SyncErrorUnknown)
	})

	t.Run("storage error from a closed repository", func(t *testing.T) {
		repo, _ := newRepo(t)
		client := &usecasetest.GitHubClient{}
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
			_ = repo.Close()
			return &model.SearchPage{Items: []model.Item{prItem("PR_1", 1, t0)}, RateLimit: model.RateLimit{Remaining: -1}}, nil
		}
		syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(func() time.Time { return t0 }))
		report, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		gt.Equal(t, report.State.LastError, model.SyncErrorStorage)
	})
}

func TestSyncWaitsAsGitHubAsks(t *testing.T) {
	ctx := context.Background()

	t.Run("Retry-After counts from the response", func(t *testing.T) {
		client, repo, clk, syncer, _ := setup(t)
		client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
			clk.Advance(40 * time.Second) // a slow request
			return nil, &interfaces.RateLimitedError{RetryAfter: 30 * time.Second}
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.Error(t, err)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.NextRunAt, t0.Add(70*time.Second))
	})

	t.Run("a forced sync keeps the poll interval of the last notification call", func(t *testing.T) {
		client, repo, clk, syncer, _ := setup(t)
		client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
			return &model.NotificationPage{NotModified: true, PollInterval: 120 * time.Second}, nil
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()

		clk.Advance(20 * time.Second)
		_, err = syncer.RunOnce(ctx, true)
		gt.NoError(t, err).Required()
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.NextRunAt, t0.Add(120*time.Second))

		t.Run("also when the forced sync fails", func(t *testing.T) {
			clk.Advance(20 * time.Second)
			client.SearchItemsFunc = func(context.Context, string, string) (*model.SearchPage, error) {
				return nil, &interfaces.APIError{StatusCode: 500}
			}
			_, err := syncer.RunOnce(ctx, true)
			gt.Error(t, err)
			st, err := repo.GetSyncState(ctx)
			gt.NoError(t, err).Required()
			gt.Equal(t, st.NextRunAt, t0.Add(120*time.Second))
		})
	})
}

func TestSyncLeaseRenewal(t *testing.T) {
	ctx := context.Background()

	t.Run("a long sync renews its lease", func(t *testing.T) {
		client, repo, clk, syncer, search := setup(t)
		search.set("author:alice", issueItem("I_1", 1, t0))
		client.SearchItemsFunc = func(ctx context.Context, query, after string) (*model.SearchPage, error) {
			clk.Advance(50 * time.Second) // five searches take longer than the lease
			return search.fn(ctx, query, after)
		}
		_, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.M(t, entries(t, repo)).Length(1)
		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.True(t, st.LeaseUntil.IsZero())
		gt.Equal(t, st.LastError, model.SyncErrorNone)
	})

	t.Run("a sync whose lease another process claimed stops writing", func(t *testing.T) {
		repo, path := newRepo(t)
		other, err := sqlite.New(ctx, path)
		gt.NoError(t, err).Required()
		t.Cleanup(func() { _ = other.Close() })

		clk := &clock{now: t0}
		client := &usecasetest.GitHubClient{}
		client.SearchItemsFunc = func(_ context.Context, query, _ string) (*model.SearchPage, error) {
			if strings.Contains(query, "review-requested:") {
				// The process stalls past its lease; another one claims it.
				clk.Advance(3 * time.Minute)
				_, ok, err := other.ClaimSync(ctx, clk.Now(), 2*time.Minute, false)
				gt.NoError(t, err)
				gt.True(t, ok)
			}
			return &model.SearchPage{Items: []model.Item{prItem("PR_1", 1, t0)}, RateLimit: model.RateLimit{Remaining: -1}}, nil
		}
		syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(clk.Now))
		report, err := syncer.RunOnce(ctx, false)
		gt.NoError(t, err).Required()
		gt.True(t, report.Skipped)
		gt.A(t, client.SearchCalls).Length(1)
		gt.M(t, entries(t, repo)).Length(0)

		st, err := repo.GetSyncState(ctx)
		gt.NoError(t, err).Required()
		gt.Equal(t, st.LeaseUntil, t0.Add(5*time.Minute))
	})
}

func TestSyncChecksTheAccount(t *testing.T) {
	ctx := context.Background()
	client, repo, _, syncer, search := setup(t)
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{prItem("PR_bob", 1, t0)})).Required()
	gt.NoError(t, repo.AddCategory(ctx, model.CategoryCreated, []model.ItemID{"PR_bob"})).Required()
	gt.NoError(t, repo.PutSyncState(ctx, &model.SyncState{Viewer: "bob", SearchCursor: t0.Add(-time.Minute), LastFullSyncAt: t0.Add(-time.Minute), RateRemaining: -1})).Required()
	search.set("author:alice", issueItem("I_alice", 2, t0))

	report, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()
	gt.True(t, report.Full)
	gt.Equal(t, client.ViewerCalls, 1)
	got := entries(t, repo)
	gt.M(t, got).Length(1)
	gt.False(t, got["I_alice"].Unread())
	gt.Equal(t, report.State.Viewer, "alice")

	// The login is asked once per process.
	_, err = syncer.RunOnce(ctx, true)
	gt.NoError(t, err)
	gt.Equal(t, client.ViewerCalls, 1)
}

func TestSyncNotificationWithNewID(t *testing.T) {
	ctx := context.Background()
	client, repo, clk, syncer, search := setup(t)
	old := issueItem("I_old", 7, t0.Add(-time.Hour))
	search.set("author:alice", old)
	_, err := syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	// The issue now has another node ID for the same repository and number.
	clk.Advance(61 * time.Second)
	search.set("author:alice")
	moved := issueItem("I_new", 7, t0)
	client.ListNotificationsFunc = func(context.Context, time.Time, string) (*model.NotificationPage, error) {
		return &model.NotificationPage{Threads: []model.NotificationThread{{Ref: old.Ref(), UpdatedAt: t0}}}, nil
	}
	client.ResolveItemsFunc = func(context.Context, []model.ItemRef) (*model.ItemsResult, error) {
		return &model.ItemsResult{Items: []model.Item{moved}, RateLimit: model.RateLimit{Remaining: -1}}, nil
	}
	_, err = syncer.RunOnce(ctx, false)
	gt.NoError(t, err).Required()

	got := entries(t, repo)
	gt.M(t, got).Length(1)
	gt.A(t, got["I_new"].Categories).Equal([]model.Category{model.CategorySubscribed})
	st, err := repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, st.LastError, model.SyncErrorNone)
}

func TestSyncUnauthorized(t *testing.T) {
	ctx := context.Background()
	client, repo, _, syncer, _ := setup(t)
	client.ViewerFunc = func(context.Context) (*model.Viewer, error) { return nil, interfaces.ErrUnauthorized }

	_, err := syncer.RunOnce(ctx, false)
	gt.Error(t, err).Is(interfaces.ErrUnauthorized)
	st, err := repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, st.LastError, model.SyncErrorUnauthorized)

	t.Run("Run stops after notifying", func(t *testing.T) {
		repo, _ := newRepo(t)
		syncer := usecase.NewSyncer(client, repo, time.Minute)
		var events []usecase.SyncEvent
		err := syncer.Run(ctx, make(chan struct{}), func(e usecase.SyncEvent) { events = append(events, e) })
		gt.Error(t, err).Is(interfaces.ErrUnauthorized)
		gt.A(t, events).Length(2).Required()
		gt.True(t, events[0].Running)
		gt.False(t, events[1].Running)
		gt.Equal(t, events[1].State.LastError, model.SyncErrorUnauthorized)
	})
}

func TestSyncRunTriggerWaitsForLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo, path := newRepo(t)
	clk := &clock{now: t0}
	syncer := usecase.NewSyncer(&usecasetest.GitHubClient{}, repo, time.Hour, usecase.WithClock(clk.Now),
		usecase.WithCheckInterval(20*time.Millisecond))

	trigger := make(chan struct{}, 1)
	log := newEventLog()
	done := make(chan error, 1)
	go func() { done <- syncer.Run(ctx, trigger, log.notify) }()
	gt.True(t, log.next(t).Running)
	gt.Equal(t, log.next(t).State.LastSyncAt, t0)

	// Another process holds the lease when r is pressed.
	other, err := sqlite.New(context.Background(), path)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = other.Close() })
	clk.Advance(time.Second)
	held, ok, err := other.ClaimSync(context.Background(), clk.Now(), 2*time.Minute, true)
	gt.NoError(t, err).Required()
	gt.True(t, ok).Required()

	trigger <- struct{}{}
	gt.True(t, log.next(t).Running)
	skipped := log.next(t)
	gt.False(t, skipped.Running)
	gt.Equal(t, skipped.State.LastSyncAt, t0)

	// The requested sync runs once the lease is released, long before the
	// next scheduled run.
	clk.Advance(time.Second)
	held.LastSyncAt = clk.Now()
	held.NextRunAt = clk.Now().Add(time.Hour)
	held.LeaseUntil = time.Time{}
	released, err := other.ReleaseSync(context.Background(), held, t0.Add(time.Second+2*time.Minute))
	gt.NoError(t, err).Required()
	gt.True(t, released).Required()

	var forced usecase.SyncEvent
	for {
		e := log.next(t)
		if e.Running {
			forced = log.next(t)
			break
		}
	}
	gt.Equal(t, forced.State.LastSyncAt, t0.Add(2*time.Second))

	cancel()
	gt.NoError(t, <-done)
}

type eventLog struct {
	mu     sync.Mutex
	events []usecase.SyncEvent
	ch     chan usecase.SyncEvent
}

func newEventLog() *eventLog {
	return &eventLog{ch: make(chan usecase.SyncEvent, 64)}
}

func (l *eventLog) notify(e usecase.SyncEvent) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
	l.ch <- e
}

func (l *eventLog) next(t *testing.T) usecase.SyncEvent {
	t.Helper()
	select {
	case e := <-l.ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no sync event")
		return usecase.SyncEvent{}
	}
}

func TestSyncRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo, path := newRepo(t)
	clk := &clock{now: t0}
	client := &usecasetest.GitHubClient{}
	syncer := usecase.NewSyncer(client, repo, time.Minute, usecase.WithClock(clk.Now),
		usecase.WithCheckInterval(20*time.Millisecond))

	trigger := make(chan struct{}, 1)
	log := newEventLog()
	done := make(chan error, 1)
	go func() { done <- syncer.Run(ctx, trigger, log.notify) }()

	// The first sync runs at start.
	gt.True(t, log.next(t).Running)
	first := log.next(t)
	gt.False(t, first.Running)
	gt.Equal(t, first.State.LastSyncAt, t0)

	// A trigger forces a sync at once, before the next scheduled run.
	clk.Advance(time.Second)
	trigger <- struct{}{}
	gt.True(t, log.next(t).Running)
	forced := log.next(t)
	gt.False(t, forced.Running)
	gt.Equal(t, forced.State.LastSyncAt, t0.Add(time.Second))
	clk.Advance(14 * time.Second)

	// Another process syncs: its result arrives by the periodic check.
	other, err := sqlite.New(context.Background(), path)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = other.Close() })
	clk.Advance(30 * time.Second)
	otherSyncer := usecase.NewSyncer(&usecasetest.GitHubClient{}, other, 10*time.Minute,
		usecase.WithClock(clk.Now))
	_, err = otherSyncer.RunOnce(context.Background(), true)
	gt.NoError(t, err).Required()
	fromOther := log.next(t)
	gt.False(t, fromOther.Running)
	gt.Equal(t, fromOther.State.LastSyncAt, t0.Add(45*time.Second))

	cancel()
	select {
	case err := <-done:
		gt.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}
