package github_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/github"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

func TestListNotifications(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []*http.Request
	)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Clone(context.Background()))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[{"updated_at":"2026-10-01T10:00:00Z","subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/34"}}]`))
			return
		}
		w.Header().Set("Last-Modified", "Thu, 01 Oct 2026 12:00:00 GMT")
		w.Header().Set("X-Poll-Interval", "60")
		w.Header().Set("Link", `<`+srvURL+`/notifications?page=2>; rel="next", <`+srvURL+`/notifications?page=2>; rel="last"`)
		_, _ = w.Write([]byte(`[
{"updated_at":"2026-10-01T11:00:00Z","subject":{"type":"Issue","url":"https://api.github.com/repos/o/r/issues/12"}},
{"updated_at":"2026-10-01T11:00:00Z","subject":{"type":"Release","url":"https://api.github.com/repos/o/r/releases/1"}},
{"updated_at":"2026-10-01T11:00:00Z","subject":{"type":"CheckSuite","url":null}},
{"updated_at":"2026-10-01T11:00:00Z","subject":{"type":"Issue","url":"https://api.github.com/repos/o/r/commits/abc"}}
]`))
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	var logBuf bytes.Buffer
	ctx := logging.With(context.Background(), slog.New(slog.NewTextHandler(&logBuf, nil)))

	client := github.New("test-token", github.WithBaseURL(srv.URL))
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	page, err := client.ListNotifications(ctx, since, "Wed, 30 Sep 2026 00:00:00 GMT")
	gt.NoError(t, err).Required()

	gt.False(t, page.NotModified)
	gt.Equal(t, page.LastModified, "Thu, 01 Oct 2026 12:00:00 GMT")
	gt.Equal(t, page.PollInterval, 60*time.Second)
	gt.A(t, page.Threads).Equal([]model.NotificationThread{
		{Ref: model.ItemRef{Repo: model.RepoName{Owner: "o", Name: "r"}, Number: 12}, UpdatedAt: time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)},
		{Ref: model.ItemRef{Repo: model.RepoName{Owner: "o", Name: "r"}, Number: 34}, UpdatedAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)},
	})
	gt.S(t, logBuf.String()).Contains("unexpected subject URL")

	gt.A(t, requests).Length(2).Required()
	q := requests[0].URL.Query()
	gt.Equal(t, q.Get("all"), "true")
	gt.Equal(t, q.Get("per_page"), "50")
	gt.Equal(t, q.Get("since"), "2026-09-01T00:00:00Z")
	gt.Equal(t, requests[0].Header.Get("If-Modified-Since"), "Wed, 30 Sep 2026 00:00:00 GMT")
	gt.Equal(t, requests[0].Header.Get("Authorization"), "bearer test-token")
}

func TestListNotificationsNotModified(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gt.Equal(t, r.Header.Get("If-Modified-Since"), "Thu, 01 Oct 2026 12:00:00 GMT")
		w.Header().Set("X-Poll-Interval", "120")
		w.Header().Set("Link", `<http://127.0.0.1/notifications?page=2>; rel="next"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)

	client := github.New("test-token", github.WithBaseURL(srv.URL))
	page, err := client.ListNotifications(context.Background(), time.Now(), "Thu, 01 Oct 2026 12:00:00 GMT")
	gt.NoError(t, err).Required()
	gt.True(t, page.NotModified)
	gt.A(t, page.Threads).Length(0)
	gt.Equal(t, page.LastModified, "")
	gt.Equal(t, page.PollInterval, 120*time.Second)
	gt.Equal(t, calls, 1)
}

func TestListNotificationsWithoutLastModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := r.Header["If-Modified-Since"]
		gt.False(t, ok)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	client := github.New("test-token", github.WithBaseURL(srv.URL))
	page, err := client.ListNotifications(context.Background(), time.Now(), "")
	gt.NoError(t, err).Required()
	gt.Equal(t, page.PollInterval, time.Duration(0))
}
