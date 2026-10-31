package github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/github"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
	Auth      string
}

// graphqlServer answers every POST /graphql with respond and records the
// requests.
type graphqlServer struct {
	mu       sync.Mutex
	requests []graphqlRequest
	respond  func(w http.ResponseWriter, req graphqlRequest)
}

func newGraphQLServer(t *testing.T, respond func(w http.ResponseWriter, req graphqlRequest)) (*graphqlServer, *github.Client) {
	t.Helper()
	s := &graphqlServer{respond: respond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req graphqlRequest
		gt.NoError(t, json.Unmarshal(body, &req))
		req.Auth = r.Header.Get("Authorization")
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		s.respond(w, req)
	}))
	t.Cleanup(srv.Close)
	return s, github.New("test-token", github.WithBaseURL(srv.URL))
}

func writeJSON(w http.ResponseWriter, v string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, v)
}

func prNode(id string, number int, ciState string, reviewDecision string, latest ...string) string {
	commits := `{"nodes":[]}`
	if ciState == "null" {
		commits = `{"nodes":[{"commit":{"statusCheckRollup":null}}]}`
	} else if ciState != "" {
		commits = fmt.Sprintf(`{"nodes":[{"commit":{"statusCheckRollup":{"state":%q}}}]}`, ciState)
	}
	decision := "null"
	if reviewDecision != "" {
		decision = fmt.Sprintf("%q", reviewDecision)
	}
	reviews := make([]string, len(latest))
	for i, s := range latest {
		reviews[i] = fmt.Sprintf(`{"state":%q}`, s)
	}
	return fmt.Sprintf(`{"__typename":"PullRequest","id":%q,"number":%d,"title":"Add retry","url":"https://github.com/example-org/api/pull/%d",
"updatedAt":"2026-10-01T12:00:00Z","state":"OPEN","isDraft":false,"reviewDecision":%s,"author":{"login":"alice"},
"repository":{"owner":{"login":"example-org"},"name":"api"},"latestReviews":{"nodes":[%s]},"commits":%s}`,
		id, number, number, decision, strings.Join(reviews, ","), commits)
}

func issueNode(id string, number int, state string) string {
	return fmt.Sprintf(`{"__typename":"Issue","id":%q,"number":%d,"title":"Crash","url":"https://github.com/octocat/hello-world/issues/%d",
"updatedAt":"2026-10-01T13:00:00Z","state":%q,"author":null,"repository":{"owner":{"login":"octocat"},"name":"hello-world"}}`,
		id, number, number, state)
}

const rateLimitJSON = `"rateLimit":{"remaining":4321,"resetAt":"2026-10-01T13:00:00Z"}`

func TestSearchItems(t *testing.T) {
	ctx := context.Background()
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"search":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[`+
			prNode("PR_1", 482, "SUCCESS", "APPROVED")+`,`+issueNode("I_1", 77, "OPEN")+`]}}}`)
	})

	page, err := client.SearchItems(ctx, "author:alice is:open", "")
	gt.NoError(t, err).Required()

	gt.A(t, srv.requests).Length(1).Required()
	req := srv.requests[0]
	gt.Equal(t, req.Variables["q"], any("author:alice is:open"))
	gt.Nil(t, req.Variables["after"])
	gt.Equal(t, req.Auth, "bearer test-token")
	gt.Equal(t, req.Variables["first"], any(float64(50)))

	gt.Equal(t, page.EndCursor, "c1")
	gt.True(t, page.HasNextPage)
	gt.Equal(t, page.RateLimit, model.RateLimit{Remaining: 4321, ResetAt: time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)})
	gt.A(t, page.Items).Length(2).Required()
	gt.Equal(t, page.Items[0], model.Item{
		ID:        "PR_1",
		Kind:      model.ItemKindPullRequest,
		Repo:      model.RepoName{Owner: "example-org", Name: "api"},
		Number:    482,
		Title:     "Add retry",
		URL:       "https://github.com/example-org/api/pull/482",
		Author:    "alice",
		State:     model.ItemStateOpen,
		CI:        model.CIStateSuccess,
		Review:    model.ReviewStateApproved,
		UpdatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	})
	gt.Equal(t, page.Items[1], model.Item{
		ID:        "I_1",
		Kind:      model.ItemKindIssue,
		Repo:      model.RepoName{Owner: "octocat", Name: "hello-world"},
		Number:    77,
		Title:     "Crash",
		URL:       "https://github.com/octocat/hello-world/issues/77",
		Author:    "ghost",
		State:     model.ItemStateOpen,
		CI:        model.CIStateNone,
		Review:    model.ReviewStateNone,
		UpdatedAt: time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC),
	})

	_, err = client.SearchItems(ctx, "author:alice", "c1")
	gt.NoError(t, err).Required()
	gt.Equal(t, srv.requests[1].Variables["after"], any("c1"))
}

func TestSearchItemsSkipsForbiddenNodes(t *testing.T) {
	ctx := context.Background()
	_, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"search":{"pageInfo":{"hasNextPage":false,"endCursor":"c1"},"nodes":[`+
			prNode("PR_1", 482, "", "")+`,null,null]}},
"errors":[{"type":"FORBIDDEN","path":["search","nodes",1],"message":"Resource protected by organization SAML enforcement."},
{"type":"FORBIDDEN","path":["search","nodes",2],"message":"Resource protected by organization SAML enforcement."}]}`)
	})

	page, err := client.SearchItems(ctx, "author:alice is:open", "")
	gt.NoError(t, err).Required()
	gt.A(t, page.Items).Length(1).Required()
	gt.Equal(t, page.Items[0].ID, model.ItemID("PR_1"))
	gt.Equal(t, page.RateLimit.Remaining, 4321)
}

func TestSearchItemsForbiddenWithoutData(t *testing.T) {
	ctx := context.Background()
	_, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":null,"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration"}]}`)
	})

	_, err := client.SearchItems(ctx, "author:alice is:open", "")
	gt.Error(t, err)
}

func TestCIAndReviewConversion(t *testing.T) {
	ctx := context.Background()
	nodes := []string{
		prNode("PR_s", 1, "SUCCESS", ""),
		prNode("PR_e", 2, "ERROR", ""),
		prNode("PR_f", 3, "FAILURE", ""),
		prNode("PR_x", 4, "EXPECTED", ""),
		prNode("PR_p", 5, "PENDING", ""),
		prNode("PR_none", 6, "", ""),
		prNode("PR_null", 7, "null", ""),
		prNode("PR_cr", 8, "", "CHANGES_REQUESTED"),
		prNode("PR_rr", 9, "", "REVIEW_REQUIRED"),
		prNode("PR_latest_cr", 10, "", "", "APPROVED", "CHANGES_REQUESTED"),
		prNode("PR_latest_ap", 11, "", "", "COMMENTED", "APPROVED"),
		prNode("PR_latest_none", 12, "", "", "COMMENTED"),
	}
	_, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[`+
			strings.Join(nodes, ",")+`]}}}`)
	})
	page, err := client.SearchItems(ctx, "q", "")
	gt.NoError(t, err).Required()
	gt.A(t, page.Items).Length(len(nodes)).Required()

	got := map[model.ItemID]model.Item{}
	for _, it := range page.Items {
		got[it.ID] = it
	}
	gt.Equal(t, got["PR_s"].CI, model.CIStateSuccess)
	gt.Equal(t, got["PR_e"].CI, model.CIStateFailure)
	gt.Equal(t, got["PR_f"].CI, model.CIStateFailure)
	gt.Equal(t, got["PR_x"].CI, model.CIStatePending)
	gt.Equal(t, got["PR_p"].CI, model.CIStatePending)
	gt.Equal(t, got["PR_none"].CI, model.CIStateNone)
	gt.Equal(t, got["PR_null"].CI, model.CIStateNone)
	gt.Equal(t, got["PR_cr"].Review, model.ReviewStateChangesRequested)
	gt.Equal(t, got["PR_rr"].Review, model.ReviewStateRequired)
	gt.Equal(t, got["PR_latest_cr"].Review, model.ReviewStateChangesRequested)
	gt.Equal(t, got["PR_latest_ap"].Review, model.ReviewStateApproved)
	gt.Equal(t, got["PR_latest_none"].Review, model.ReviewStateNone)
	gt.Equal(t, got["PR_s"].Review, model.ReviewStateNone)
}

func TestViewerReviewConversion(t *testing.T) {
	ctx := context.Background()
	withViewer := func(node, review, request string) string {
		return strings.Replace(node, `"latestReviews":`,
			`"viewerLatestReview":`+review+`,"viewerLatestReviewRequest":`+request+`,"latestReviews":`, 1)
	}
	approved := `{"state":"APPROVED","submittedAt":"2026-10-01T11:00:00Z"}`
	nodes := []string{
		withViewer(prNode("PR_approved", 1, "", ""), approved, `null`),
		withViewer(prNode("PR_rerequested", 2, "", ""), approved, `{"id":"RR_1"}`),
		withViewer(prNode("PR_commented", 3, "", ""), `{"state":"COMMENTED","submittedAt":"2026-10-01T11:00:00Z"}`, `null`),
		withViewer(prNode("PR_dismissed", 4, "", ""), `{"state":"DISMISSED","submittedAt":"2026-10-01T11:00:00Z"}`, `null`),
		withViewer(prNode("PR_requested", 5, "", ""), `null`, `{"id":"RR_2"}`),
		prNode("PR_absent", 6, "", ""),
		issueNode("I_1", 7, "OPEN"),
	}
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[`+
			strings.Join(nodes, ",")+`]}}}`)
	})
	page, err := client.SearchItems(ctx, "q", "")
	gt.NoError(t, err).Required()
	gt.S(t, srv.requests[0].Query).Contains("viewerLatestReview { state submittedAt }")
	gt.S(t, srv.requests[0].Query).Contains("viewerLatestReviewRequest { id }")
	gt.A(t, page.Items).Length(len(nodes)).Required()

	type viewerReview struct {
		approvedAt time.Time
		requested  bool
	}
	got := map[model.ItemID]viewerReview{}
	for _, it := range page.Items {
		gt.True(t, it.ViewerAddressedAt.IsZero())
		got[it.ID] = viewerReview{approvedAt: it.ViewerApprovedAt, requested: it.ViewerReviewRequested}
	}
	approvedAt := time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	gt.Equal(t, got, map[model.ItemID]viewerReview{
		"PR_approved":    {approvedAt: approvedAt},
		"PR_rerequested": {approvedAt: approvedAt, requested: true},
		"PR_commented":   {},
		"PR_dismissed":   {},
		"PR_requested":   {requested: true},
		"PR_absent":      {},
		"I_1":            {},
	})
}

func TestListComments(t *testing.T) {
	ctx := context.Background()
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,
"p0":{"timelineItems":{"pageInfo":{"hasPreviousPage":true,"startCursor":"T1"},"nodes":[
  {"__typename":"IssueComment","createdAt":"2026-10-01T12:00:00Z","body":"@alice ping","author":{"login":"bob"}},
  {"__typename":"PullRequestReview","id":"PRR_1","createdAt":"2026-10-01T12:05:00Z","body":"","author":null,"comments":{
    "pageInfo":{"hasPreviousPage":true,"startCursor":"C1"},"nodes":[
    {"createdAt":"2026-10-01T12:05:00Z","body":"Fixed.","author":{"login":"bob"},"replyTo":{"author":{"login":"alice"}}},
    {"createdAt":"2026-10-01T12:06:00Z","body":"nit","author":{"login":"carol"},"replyTo":null}
  ]}},
  {"__typename":"PullRequestReview","id":"PRR_2","createdAt":"2026-10-01T12:07:00Z","body":"ok","author":{"login":"carol"},"comments":{
    "pageInfo":{"hasPreviousPage":false,"startCursor":null},"nodes":[]}}
]}},
"p1":null,
"p2":{"comments":{"pageInfo":{"hasPreviousPage":false,"startCursor":"C9"},"nodes":[
  {"createdAt":"2026-10-01T11:30:00Z","body":"older","author":{"login":"bob"},"replyTo":{"author":{"login":"alice"}}}
]}}}}`)
	})
	since := time.Date(2026, 10, 1, 11, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	queries := []model.CommentQuery{
		{ID: "PR_1", Since: since},
		{ID: "PR_gone", Since: since},
		{ID: "PR_3", Since: since, ReviewID: "PRR_9", Before: "C10"},
	}
	result, err := client.ListComments(ctx, queries)
	gt.NoError(t, err).Required()

	gt.A(t, srv.requests).Length(1).Required()
	req := srv.requests[0]
	gt.Equal(t, req.Variables["i0"], any("PR_1"))
	gt.Equal(t, req.Variables["s0"], any("2026-10-01T02:00:00Z"))
	gt.Nil(t, req.Variables["b0"])
	gt.Equal(t, req.Variables["i1"], any("PR_gone"))
	gt.Equal(t, req.Variables["i2"], any("PRR_9"))
	gt.Equal(t, req.Variables["b2"], any("C10"))
	_, hasSince := req.Variables["s2"]
	gt.False(t, hasSince)
	gt.S(t, req.Query).Contains("p0: node(id: $i0) { ... on PullRequest {")
	gt.S(t, req.Query).Contains("timelineItems(since: $s1, last: 50, before: $b1, itemTypes: [ISSUE_COMMENT, PULL_REQUEST_REVIEW])")
	gt.S(t, req.Query).Contains("p2: node(id: $i2) { ... on PullRequestReview {")
	gt.S(t, req.Query).Contains("comments(last: 20, before: $b2)")

	gt.Equal(t, result.RateLimit.Remaining, 4321)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 1, h, m, 0, 0, time.UTC) }
	gt.Equal(t, result.Pages, []model.CommentPage{
		{
			Query: queries[0],
			Found: true,
			Comments: []model.Comment{
				{Author: "bob", Body: "@alice ping", CreatedAt: at(12, 0)},
				{Author: "ghost", Body: "", CreatedAt: at(12, 5)},
				{Author: "bob", Body: "Fixed.", CreatedAt: at(12, 5), ReplyToAuthor: "alice"},
				{Author: "carol", Body: "nit", CreatedAt: at(12, 6)},
				{Author: "carol", Body: "ok", CreatedAt: at(12, 7)},
			},
			Next: []model.CommentQuery{
				{ID: "PR_1", Since: since, ReviewID: "PRR_1", Before: "C1"},
				{ID: "PR_1", Since: since, Before: "T1"},
			},
		},
		{Query: queries[1]},
		{
			Query:    queries[2],
			Found:    true,
			Comments: []model.Comment{{Author: "bob", Body: "older", CreatedAt: at(11, 30), ReplyToAuthor: "alice"}},
		},
	})
}

func TestListCommentsLimits(t *testing.T) {
	ctx := context.Background()
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{}}`)
	})

	result, err := client.ListComments(ctx, nil)
	gt.NoError(t, err).Required()
	gt.A(t, result.Pages).Length(0)

	queries := make([]model.CommentQuery, 11)
	for i := range queries {
		queries[i] = model.CommentQuery{ID: model.ItemID(fmt.Sprintf("PR_%d", i)), Since: time.Now()}
	}
	_, err = client.ListComments(ctx, queries)
	gt.Error(t, err)
	gt.A(t, srv.requests).Length(0)
}

func TestClosedAndMergedStates(t *testing.T) {
	ctx := context.Background()
	merged := strings.Replace(prNode("PR_m", 1, "", ""), `"state":"OPEN"`, `"state":"MERGED"`, 1)
	_, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"nodes":[`+merged+`,`+issueNode("I_c", 2, "CLOSED")+`]}}`)
	})
	result, err := client.GetItems(ctx, []model.ItemID{"PR_m", "I_c"})
	gt.NoError(t, err).Required()
	gt.A(t, result.Items).Length(2).Required()
	gt.Equal(t, result.Items[0].State, model.ItemStateMerged)
	gt.Equal(t, result.Items[1].State, model.ItemStateClosed)
}

func TestArchivedRepository(t *testing.T) {
	ctx := context.Background()
	archived := strings.Replace(issueNode("I_a", 1, "OPEN"), `"name":"hello-world"`, `"name":"hello-world","isArchived":true`, 1)
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"nodes":[`+archived+`,`+prNode("PR_1", 2, "", "")+`]}}`)
	})
	result, err := client.GetItems(ctx, []model.ItemID{"I_a", "PR_1"})
	gt.NoError(t, err).Required()
	gt.S(t, srv.requests[0].Query).Contains("isArchived")
	gt.A(t, result.Items).Length(2).Required()
	gt.True(t, result.Items[0].RepoArchived)
	gt.Equal(t, result.Items[0].State, model.ItemStateOpen)
	gt.False(t, result.Items[1].RepoArchived)
}

func TestGetItems(t *testing.T) {
	ctx := context.Background()
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,"nodes":[`+prNode("PR_1", 1, "", "")+`,null]},
"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a node with the global id of 'PR_gone'"}]}`)
	})

	result, err := client.GetItems(ctx, []model.ItemID{"PR_1", "PR_gone"})
	gt.NoError(t, err).Required()
	gt.A(t, result.Items).Length(1)
	gt.Equal(t, result.Items[0].ID, model.ItemID("PR_1"))
	gt.A(t, result.MissingIDs).Equal([]model.ItemID{"PR_gone"})
	gt.Equal(t, result.RateLimit.Remaining, 4321)
	gt.A(t, srv.requests[0].Variables["ids"].([]any)).Equal([]any{"PR_1", "PR_gone"})

	ids := make([]model.ItemID, 51)
	for i := range ids {
		ids[i] = model.ItemID(fmt.Sprintf("PR_%d", i))
	}
	_, err = client.GetItems(ctx, ids)
	gt.Error(t, err)
	gt.A(t, srv.requests).Length(1)
}

func TestResolveItems(t *testing.T) {
	ctx := context.Background()
	srv, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{`+rateLimitJSON+`,
"r0":{"issueOrPullRequest":`+prNode("PR_1", 12, "", "")+`},
"r1":{"issueOrPullRequest":null},
"r2":null}}`)
	})

	refs := []model.ItemRef{
		{Repo: model.RepoName{Owner: "example-org", Name: "api"}, Number: 12},
		{Repo: model.RepoName{Owner: "example-org", Name: "api"}, Number: 34},
		{Repo: model.RepoName{Owner: "example-org", Name: "gone"}, Number: 1},
	}
	result, err := client.ResolveItems(ctx, refs)
	gt.NoError(t, err).Required()
	gt.A(t, result.Items).Length(1)
	gt.Equal(t, result.Items[0].ID, model.ItemID("PR_1"))
	gt.Equal(t, result.RateLimit.Remaining, 4321)

	req := srv.requests[0]
	gt.S(t, req.Query).Contains("r0: repository(owner: $o0, name: $r0)")
	gt.S(t, req.Query).Contains("r1: repository(owner: $o1, name: $r1)")
	gt.S(t, req.Query).Contains("$o0: String!, $r0: String!, $n0: Int!")
	gt.Equal(t, req.Variables["o0"], any("example-org"))
	gt.Equal(t, req.Variables["r1"], any("api"))
	gt.Equal(t, req.Variables["n1"], any(float64(34)))

	refs = make([]model.ItemRef, 51)
	for i := range refs {
		refs[i] = model.ItemRef{Repo: model.RepoName{Owner: "o", Name: "r"}, Number: i + 1}
	}
	_, err = client.ResolveItems(ctx, refs)
	gt.Error(t, err)
	gt.A(t, srv.requests).Length(1)
}

func TestViewer(t *testing.T) {
	_, client := newGraphQLServer(t, func(w http.ResponseWriter, _ graphqlRequest) {
		writeJSON(w, `{"data":{"viewer":{"login":"alice"}}}`)
	})
	v, err := client.Viewer(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, v.Login, "alice")
}
