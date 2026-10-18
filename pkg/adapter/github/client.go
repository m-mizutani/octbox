// Package github calls the GitHub GraphQL API, the notifications REST API,
// and the OAuth device flow, and converts the responses into domain models.
// It makes no decision about what to fetch.
package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

const DefaultAPIBaseURL = "https://api.github.com"

// graphqlRateLimitWait is how long to wait after a GraphQL RATE_LIMITED
// error, which carries no Retry-After header.
const graphqlRateLimitWait = time.Minute

// Client implements interfaces.GitHubClient.
type Client struct {
	token      model.GitHubToken
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

var _ interfaces.GitHubClient = (*Client)(nil)

type Option func(*Client)

// WithBaseURL replaces https://api.github.com, for tests.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// requestTimeout bounds one HTTP request. The syncer renews its lease when
// less than a minute is left, so a request must end well within that.
const requestTimeout = 30 * time.Second

func New(token model.GitHubToken, opts ...Option) *Client {
	c := &Client{
		token:      token,
		baseURL:    DefaultAPIBaseURL,
		httpClient: &http.Client{Timeout: requestTimeout},
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create a request", goerr.V("url", url))
	}
	req.Header.Set("Authorization", "bearer "+string(c.token))
	req.Header.Set("User-Agent", "octbox")
	return req, nil
}

// do sends req and converts an error status into a domain error. The caller
// closes the body of the returned response.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to call GitHub", goerr.V("url", req.URL.String()))
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer safe.Close(req.Context(), resp.Body)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	values := []goerr.Option{goerr.V("url", req.URL.String()), goerr.V("status", resp.StatusCode)}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, goerr.Wrap(interfaces.ErrUnauthorized, "GitHub answered 401", values...)
	case http.StatusForbidden, http.StatusTooManyRequests:
		if wait, ok := c.rateLimitWait(resp.Header); ok {
			return nil, goerr.Wrap(&interfaces.RateLimitedError{RetryAfter: wait}, "GitHub rate limit", values...)
		}
	}
	return nil, goerr.Wrap(&interfaces.APIError{StatusCode: resp.StatusCode}, "GitHub API error", values...)
}

// rateLimitWait reads Retry-After (secondary rate limit) or, when the primary
// limit is exhausted, the time until X-RateLimit-Reset.
func (c *Client) rateLimitWait(h http.Header) (time.Duration, bool) {
	if v := h.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec >= 0 {
			return time.Duration(sec) * time.Second, true
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			wait := time.Unix(reset, 0).Sub(c.now())
			if wait < 0 {
				wait = 0
			}
			return wait, true
		}
		return graphqlRateLimitWait, true
	}
	return 0, false
}

type graphqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphqlError  `json:"errors"`
}

// graphql posts query and decodes data into out. NOT_FOUND errors are not
// failures when data is present: the missing nodes come back as null.
func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return goerr.Wrap(err, "failed to encode a GraphQL request")
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.baseURL+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer safe.Close(ctx, resp.Body)

	var gr graphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return goerr.Wrap(err, "failed to decode a GraphQL response")
	}

	onlyNotFound := true
	for _, e := range gr.Errors {
		if e.Type == "RATE_LIMITED" {
			return goerr.Wrap(&interfaces.RateLimitedError{RetryAfter: graphqlRateLimitWait},
				"GitHub GraphQL rate limit", goerr.V("message", e.Message))
		}
		if e.Type != "NOT_FOUND" {
			onlyNotFound = false
		}
	}
	hasData := len(gr.Data) > 0 && string(gr.Data) != "null"
	if len(gr.Errors) > 0 && (!onlyNotFound || !hasData) {
		types := make([]string, 0, len(gr.Errors))
		for _, e := range gr.Errors {
			types = append(types, e.Type)
		}
		return goerr.Wrap(&interfaces.APIError{StatusCode: resp.StatusCode}, "GitHub GraphQL error",
			goerr.V("error_types", types), goerr.V("message", gr.Errors[0].Message))
	}
	if !hasData {
		return goerr.Wrap(&interfaces.APIError{StatusCode: resp.StatusCode}, "GitHub GraphQL response has no data")
	}
	if err := json.Unmarshal(gr.Data, out); err != nil {
		return goerr.Wrap(err, "failed to decode GraphQL data")
	}
	return nil
}
