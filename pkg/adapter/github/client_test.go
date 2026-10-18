package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/github"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

func asRateLimited(t *testing.T, err error) *interfaces.RateLimitedError {
	t.Helper()
	var e *interfaces.RateLimitedError
	gt.True(t, errors.As(err, &e)).Required()
	return e
}

func asAPIError(t *testing.T, err error) *interfaces.APIError {
	t.Helper()
	var e *interfaces.APIError
	gt.True(t, errors.As(err, &e)).Required()
	return e
}

// callAll runs every client method against handler and returns their errors
// in the order Viewer, SearchItems, GetItems, ResolveItems, ListNotifications.
func callAll(t *testing.T, handler http.HandlerFunc) []error {
	t.Helper()
	ctx := context.Background()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := github.New("test-token", github.WithBaseURL(srv.URL))

	var errs []error
	_, err := c.Viewer(ctx)
	errs = append(errs, err)
	_, err = c.SearchItems(ctx, "q", "")
	errs = append(errs, err)
	_, err = c.GetItems(ctx, []model.ItemID{"PR_1"})
	errs = append(errs, err)
	_, err = c.ResolveItems(ctx, []model.ItemRef{{Repo: model.RepoName{Owner: "o", Name: "r"}, Number: 1}})
	errs = append(errs, err)
	_, err = c.ListNotifications(ctx, time.Now(), "")
	errs = append(errs, err)
	return errs
}

func TestErrorMapping401(t *testing.T) {
	for _, err := range callAll(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}) {
		gt.Error(t, err).Is(interfaces.ErrUnauthorized)
	}
}

func TestErrorMappingRetryAfter(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			for _, err := range callAll(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(status)
			}) {
				gt.Equal(t, asRateLimited(t, err).RetryAfter, 30*time.Second)
			}
		})
	}
}

func TestErrorMappingExhaustedPrimaryLimit(t *testing.T) {
	reset := time.Now().Add(120 * time.Second).Unix()
	for _, err := range callAll(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		w.WriteHeader(http.StatusForbidden)
	}) {
		wait := asRateLimited(t, err).RetryAfter
		gt.True(t, wait > 100*time.Second && wait <= 120*time.Second)
	}
}

func TestErrorMappingGraphQLRateLimited(t *testing.T) {
	errs := callAll(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		writeJSON(w, `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`)
	})
	for _, err := range errs[:4] {
		gt.Equal(t, asRateLimited(t, err).RetryAfter, 60*time.Second)
	}
}

func TestErrorMappingOtherStatus(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusForbidden} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			for _, err := range callAll(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}) {
				gt.Equal(t, asAPIError(t, err).StatusCode, status)
			}
		})
	}
}

func TestErrorMappingGraphQLErrors(t *testing.T) {
	errs := callAll(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		writeJSON(w, `{"data":{"viewer":null},"errors":[{"type":"NOT_FOUND","message":"x"},{"type":"FORBIDDEN","message":"y"}]}`)
	})
	for _, err := range errs[:4] {
		gt.Equal(t, asAPIError(t, err).StatusCode, http.StatusOK)
	}
}
