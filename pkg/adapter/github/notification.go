package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

type notificationJSON struct {
	UpdatedAt time.Time `json:"updated_at"`
	Subject   struct {
		Type string  `json:"type"`
		URL  *string `json:"url"`
	} `json:"subject"`
}

func (c *Client) ListNotifications(ctx context.Context, since time.Time, lastModified string) (*model.NotificationPage, error) {
	q := url.Values{}
	q.Set("all", "true")
	q.Set("per_page", "50")
	q.Set("since", since.UTC().Format(time.RFC3339))
	next := c.baseURL + "/notifications?" + q.Encode()

	page := &model.NotificationPage{}
	for first := true; next != ""; first = false {
		req, err := c.newRequest(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if first && lastModified != "" {
			req.Header.Set("If-Modified-Since", lastModified)
		}

		resp, err := c.do(req)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list notifications")
		}

		if first {
			if v, err := strconv.Atoi(resp.Header.Get("X-Poll-Interval")); err == nil && v > 0 {
				page.PollInterval = time.Duration(v) * time.Second
			}
			if resp.StatusCode == http.StatusNotModified {
				safe.Close(ctx, resp.Body)
				page.NotModified = true
				return page, nil
			}
			page.LastModified = resp.Header.Get("Last-Modified")
		}

		var threads []notificationJSON
		err = json.NewDecoder(resp.Body).Decode(&threads)
		safe.Close(ctx, resp.Body)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to decode notifications")
		}
		for _, th := range threads {
			if th.Subject.Type != "Issue" && th.Subject.Type != "PullRequest" {
				continue
			}
			subjectURL := ""
			if th.Subject.URL != nil {
				subjectURL = *th.Subject.URL
			}
			ref, ok := parseSubjectURL(subjectURL)
			if !ok {
				logging.From(ctx).Warn("ignored a notification with an unexpected subject URL",
					"subject_type", th.Subject.Type, "subject_url", subjectURL)
				continue
			}
			page.Threads = append(page.Threads, model.NotificationThread{Ref: ref, UpdatedAt: th.UpdatedAt.UTC()})
		}

		next = nextLink(resp.Header.Get("Link"))
	}
	return page, nil
}

// parseSubjectURL reads https://api.github.com/repos/{owner}/{name}/(issues|pulls)/{number}.
func parseSubjectURL(raw string) (model.ItemRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return model.ItemRef{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "repos" || (parts[3] != "issues" && parts[3] != "pulls") {
		return model.ItemRef{}, false
	}
	number, err := strconv.Atoi(parts[4])
	if err != nil || number <= 0 || parts[1] == "" || parts[2] == "" {
		return model.ItemRef{}, false
	}
	return model.ItemRef{Repo: model.RepoName{Owner: parts[1], Name: parts[2]}, Number: number}, true
}

// nextLink returns the rel="next" URL of a Link header, or "".
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		target := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		for _, attr := range segments[1:] {
			if strings.TrimSpace(attr) == `rel="next"` {
				return strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
			}
		}
	}
	return ""
}
