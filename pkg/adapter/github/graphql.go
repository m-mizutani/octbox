package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// Page and batch sizes are capped at 50: GitHub answers 502 when a query runs
// past its 10-second limit, and the CI rollup and reviews of each pull
// request make every node expensive (50 pull requests took up to 6.5
// seconds). Pages of a search are fetched one after another, so a larger
// page saves round trips: 61 items took 3.6 seconds for the first of three
// pages of 25 and 5.1 seconds for one page.
const (
	maxGetItems     = 50
	maxResolveItems = 50
	searchPageSize  = 50
)

// Comments are fetched only for pull requests the viewer approved and that
// were updated since. A request for 2 pull requests cost 1 point; the nested
// connections (50 timeline items x 20 line comments) can make a node large,
// so a request holds at most 10 queries to stay under GitHub's 10-second
// limit.
const (
	maxCommentQueries    = 10
	commentTimelineItems = 50
	commentLineComments  = 20
)

const lineCommentsSelection = `pageInfo { hasPreviousPage startCursor }
        nodes { createdAt body author { login } replyTo { author { login } } }`

// timelineSelection and reviewSelection select one aliased node; their
// arguments are the alias index, the page size, and lineCommentsSelection.
// Each query has its own variables, which a fragment cannot take.
const (
	timelineSelection = `  p%[1]d: node(id: $i%[1]d) { ... on PullRequest {
    timelineItems(since: $s%[1]d, last: %[2]d, before: $b%[1]d, itemTypes: [ISSUE_COMMENT, PULL_REQUEST_REVIEW]) {
      pageInfo { hasPreviousPage startCursor }
      nodes {
        __typename
        ... on IssueComment { createdAt body author { login } }
        ... on PullRequestReview { id createdAt body author { login }
          comments(last: %[4]d) { %[3]s } }
      }
    }
  } }
`
	reviewSelection = `  p%[1]d: node(id: $i%[1]d) { ... on PullRequestReview {
    comments(last: %[2]d, before: $b%[1]d) { %[3]s }
  } }
`
)

const itemFieldsFragment = `
fragment ItemFields on Node {
  __typename
  id
  ... on Issue {
    number title url updatedAt state
    author { login }
    repository { owner { login } name isArchived }
  }
  ... on PullRequest {
    number title url updatedAt state isDraft reviewDecision
    author { login }
    repository { owner { login } name isArchived }
    latestReviews(first: 10) { nodes { state } }
    viewerLatestReview { state submittedAt }
    viewerLatestReviewRequest { id }
    commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  }
}`

const searchQuery = `
query Search($q: String!, $first: Int!, $after: String) {
  rateLimit { remaining resetAt }
  search(query: $q, type: ISSUE, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ...ItemFields }
  }
}` + itemFieldsFragment

const nodesQuery = `
query Nodes($ids: [ID!]!) {
  rateLimit { remaining resetAt }
  nodes(ids: $ids) { ...ItemFields }
}` + itemFieldsFragment

const viewerQuery = `query Viewer { viewer { login } }`

type rateLimitNode struct {
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

func (x *rateLimitNode) toModel() model.RateLimit {
	if x == nil {
		return model.RateLimit{Remaining: -1}
	}
	return model.RateLimit{Remaining: x.Remaining, ResetAt: x.ResetAt.UTC()}
}

type itemNode struct {
	TypeName       string    `json:"__typename"`
	ID             string    `json:"id"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	UpdatedAt      time.Time `json:"updatedAt"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"isDraft"`
	ReviewDecision *string   `json:"reviewDecision"`
	Author         *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name       string `json:"name"`
		IsArchived bool   `json:"isArchived"`
	} `json:"repository"`
	LatestReviews struct {
		Nodes []struct {
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ViewerLatestReview *struct {
		State       string    `json:"state"`
		SubmittedAt time.Time `json:"submittedAt"`
	} `json:"viewerLatestReview"`
	ViewerLatestReviewRequest *struct {
		ID string `json:"id"`
	} `json:"viewerLatestReviewRequest"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// toModel converts an Issue or PullRequest node. ok is false for any other
// node type.
func (x *itemNode) toModel() (model.Item, bool, error) {
	var kind model.ItemKind
	switch x.TypeName {
	case "Issue":
		kind = model.ItemKindIssue
	case "PullRequest":
		kind = model.ItemKindPullRequest
	default:
		return model.Item{}, false, nil
	}

	item := model.Item{
		ID:           model.ItemID(x.ID),
		Kind:         kind,
		Repo:         model.RepoName{Owner: x.Repository.Owner.Login, Name: x.Repository.Name},
		Number:       x.Number,
		Title:        x.Title,
		URL:          x.URL,
		Author:       "ghost",
		CI:           model.CIStateNone,
		Review:       model.ReviewStateNone,
		UpdatedAt:    x.UpdatedAt.UTC(),
		RepoArchived: x.Repository.IsArchived,
	}
	if x.Author != nil {
		item.Author = x.Author.Login
	}

	switch x.State {
	case "OPEN":
		item.State = model.ItemStateOpen
	case "CLOSED":
		item.State = model.ItemStateClosed
	case "MERGED":
		item.State = model.ItemStateMerged
	default:
		return model.Item{}, false, goerr.New("unknown item state", goerr.V("item_id", x.ID), goerr.V("state", x.State))
	}

	if kind == model.ItemKindPullRequest {
		item.IsDraft = x.IsDraft
		item.CI = x.ciState()
		item.Review = x.reviewState()
		if r := x.ViewerLatestReview; r != nil && r.State == "APPROVED" {
			item.ViewerApprovedAt = r.SubmittedAt.UTC()
		}
		item.ViewerReviewRequested = x.ViewerLatestReviewRequest != nil
	}

	if err := item.Validate(); err != nil {
		return model.Item{}, false, goerr.Wrap(err, "GitHub returned an invalid item")
	}
	return item, true, nil
}

func (x *itemNode) ciState() model.CIState {
	if len(x.Commits.Nodes) == 0 || x.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return model.CIStateNone
	}
	switch x.Commits.Nodes[0].Commit.StatusCheckRollup.State {
	case "SUCCESS":
		return model.CIStateSuccess
	case "FAILURE", "ERROR":
		return model.CIStateFailure
	case "PENDING", "EXPECTED":
		return model.CIStatePending
	default:
		return model.CIStateNone
	}
}

func (x *itemNode) reviewState() model.ReviewState {
	if x.ReviewDecision != nil {
		switch *x.ReviewDecision {
		case "APPROVED":
			return model.ReviewStateApproved
		case "CHANGES_REQUESTED":
			return model.ReviewStateChangesRequested
		case "REVIEW_REQUIRED":
			return model.ReviewStateRequired
		}
	}
	// reviewDecision is null for repositories without required reviews.
	approved := false
	for _, r := range x.LatestReviews.Nodes {
		switch r.State {
		case "CHANGES_REQUESTED":
			return model.ReviewStateChangesRequested
		case "APPROVED":
			approved = true
		}
	}
	if approved {
		return model.ReviewStateApproved
	}
	return model.ReviewStateNone
}

func convertNodes(nodes []*itemNode) ([]model.Item, error) {
	items := make([]model.Item, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		item, ok, err := n.toModel()
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func (c *Client) Viewer(ctx context.Context) (*model.Viewer, error) {
	var data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := c.graphql(ctx, viewerQuery, nil, &data); err != nil {
		return nil, goerr.Wrap(err, "failed to get the viewer")
	}
	if data.Viewer.Login == "" {
		return nil, goerr.New("GitHub returned an empty login")
	}
	return &model.Viewer{Login: data.Viewer.Login}, nil
}

func (c *Client) SearchItems(ctx context.Context, query string, after string) (*model.SearchPage, error) {
	vars := map[string]any{"q": query, "first": searchPageSize, "after": nil}
	if after != "" {
		vars["after"] = after
	}
	var data struct {
		RateLimit *rateLimitNode `json:"rateLimit"`
		Search    struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []*itemNode `json:"nodes"`
		} `json:"search"`
	}
	if err := c.graphql(ctx, searchQuery, vars, &data); err != nil {
		return nil, goerr.Wrap(err, "failed to search items", goerr.V("query", query))
	}
	items, err := convertNodes(data.Search.Nodes)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to convert search results", goerr.V("query", query))
	}
	return &model.SearchPage{
		Items:       items,
		EndCursor:   data.Search.PageInfo.EndCursor,
		HasNextPage: data.Search.PageInfo.HasNextPage,
		RateLimit:   data.RateLimit.toModel(),
	}, nil
}

func (c *Client) GetItems(ctx context.Context, ids []model.ItemID) (*model.ItemsResult, error) {
	if len(ids) > maxGetItems {
		return nil, goerr.New("too many item IDs", goerr.V("count", len(ids)), goerr.V("max", maxGetItems))
	}
	if len(ids) == 0 {
		return &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}, nil
	}

	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = string(id)
	}
	var data struct {
		RateLimit *rateLimitNode `json:"rateLimit"`
		Nodes     []*itemNode    `json:"nodes"`
	}
	if err := c.graphql(ctx, nodesQuery, map[string]any{"ids": strIDs}, &data); err != nil {
		return nil, goerr.Wrap(err, "failed to get items", goerr.V("count", len(ids)))
	}

	result := &model.ItemsResult{RateLimit: data.RateLimit.toModel()}
	for i, id := range ids {
		if i >= len(data.Nodes) || data.Nodes[i] == nil {
			result.MissingIDs = append(result.MissingIDs, id)
		}
	}
	items, err := convertNodes(data.Nodes)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to convert items")
	}
	result.Items = items
	return result, nil
}

// buildResolveQuery returns a query with one aliased repository field per
// ref, r0..r{n-1}, and its variables.
func buildResolveQuery(refs []model.ItemRef) (string, map[string]any) {
	var params, fields strings.Builder
	vars := make(map[string]any, len(refs)*3)
	for i, ref := range refs {
		if i > 0 {
			params.WriteString(", ")
		}
		fmt.Fprintf(&params, "$o%d: String!, $r%d: String!, $n%d: Int!", i, i, i)
		fmt.Fprintf(&fields, "  r%d: repository(owner: $o%d, name: $r%d) { issueOrPullRequest(number: $n%d) { ...ItemFields } }\n", i, i, i, i)
		vars[fmt.Sprintf("o%d", i)] = ref.Repo.Owner
		vars[fmt.Sprintf("r%d", i)] = ref.Repo.Name
		vars[fmt.Sprintf("n%d", i)] = ref.Number
	}
	query := "query Resolve(" + params.String() + ") {\n  rateLimit { remaining resetAt }\n" +
		fields.String() + "}" + itemFieldsFragment
	return query, vars
}

func (c *Client) ResolveItems(ctx context.Context, refs []model.ItemRef) (*model.ItemsResult, error) {
	if len(refs) > maxResolveItems {
		return nil, goerr.New("too many item refs", goerr.V("count", len(refs)), goerr.V("max", maxResolveItems))
	}
	if len(refs) == 0 {
		return &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}, nil
	}

	query, vars := buildResolveQuery(refs)
	var data map[string]json.RawMessage
	if err := c.graphql(ctx, query, vars, &data); err != nil {
		return nil, goerr.Wrap(err, "failed to resolve items", goerr.V("count", len(refs)))
	}

	result := &model.ItemsResult{RateLimit: model.RateLimit{Remaining: -1}}
	if raw, ok := data["rateLimit"]; ok {
		var rl *rateLimitNode
		if err := json.Unmarshal(raw, &rl); err != nil {
			return nil, goerr.Wrap(err, "failed to decode the rate limit")
		}
		result.RateLimit = rl.toModel()
	}

	nodes := make([]*itemNode, 0, len(refs))
	for i := range refs {
		raw, ok := data[fmt.Sprintf("r%d", i)]
		if !ok {
			continue
		}
		var repo *struct {
			IssueOrPullRequest *itemNode `json:"issueOrPullRequest"`
		}
		if err := json.Unmarshal(raw, &repo); err != nil {
			return nil, goerr.Wrap(err, "failed to decode a resolved item", goerr.V("index", i))
		}
		if repo != nil && repo.IssueOrPullRequest != nil {
			nodes = append(nodes, repo.IssueOrPullRequest)
		}
	}
	items, err := convertNodes(nodes)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to convert resolved items")
	}
	result.Items = items
	return result, nil
}

type authorNode struct {
	Login string `json:"login"`
}

func loginOf(a *authorNode) string {
	if a == nil {
		return "ghost"
	}
	return a.Login
}

type commentNode struct {
	CreatedAt time.Time   `json:"createdAt"`
	Body      string      `json:"body"`
	Author    *authorNode `json:"author"`
	ReplyTo   *struct {
		Author *authorNode `json:"author"`
	} `json:"replyTo"`
}

func (x *commentNode) toModel() model.Comment {
	c := model.Comment{Author: loginOf(x.Author), Body: x.Body, CreatedAt: x.CreatedAt.UTC()}
	if x.ReplyTo != nil {
		c.ReplyToAuthor = loginOf(x.ReplyTo.Author)
	}
	return c
}

type pageInfoNode struct {
	HasPreviousPage bool   `json:"hasPreviousPage"`
	StartCursor     string `json:"startCursor"`
}

type lineCommentsNode struct {
	PageInfo pageInfoNode   `json:"pageInfo"`
	Nodes    []*commentNode `json:"nodes"`
}

// commentsNode is a pull request answering timelineSelection, or a review
// answering reviewSelection.
type commentsNode struct {
	TimelineItems struct {
		PageInfo pageInfoNode `json:"pageInfo"`
		Nodes    []*struct {
			TypeName string `json:"__typename"`
			ID       string `json:"id"`
			commentNode
			Comments lineCommentsNode `json:"comments"`
		} `json:"nodes"`
	} `json:"timelineItems"`
	Comments lineCommentsNode `json:"comments"`
}

// buildCommentsQuery returns a query with one aliased node per query,
// p0..p{n-1}, and its variables.
func buildCommentsQuery(queries []model.CommentQuery) (string, map[string]any) {
	var params, fields strings.Builder
	vars := make(map[string]any, len(queries)*3)
	for i, q := range queries {
		if i > 0 {
			params.WriteString(", ")
		}
		var before any
		if q.Before != "" {
			before = q.Before
		}
		vars[fmt.Sprintf("b%d", i)] = before
		if q.ReviewID == "" {
			fmt.Fprintf(&params, "$i%d: ID!, $s%d: DateTime!, $b%d: String", i, i, i)
			fmt.Fprintf(&fields, timelineSelection, i, commentTimelineItems, lineCommentsSelection, commentLineComments)
			vars[fmt.Sprintf("i%d", i)] = string(q.ID)
			vars[fmt.Sprintf("s%d", i)] = q.Since.UTC().Format(time.RFC3339)
		} else {
			fmt.Fprintf(&params, "$i%d: ID!, $b%d: String", i, i)
			fmt.Fprintf(&fields, reviewSelection, i, commentLineComments, lineCommentsSelection)
			vars[fmt.Sprintf("i%d", i)] = q.ReviewID
		}
	}
	query := "query Comments(" + params.String() + ") {\n  rateLimit { remaining resetAt }\n" + fields.String() + "}"
	return query, vars
}

func appendLineComments(page *model.CommentPage, reviewID string, lc *lineCommentsNode) {
	for _, n := range lc.Nodes {
		if n != nil {
			page.Comments = append(page.Comments, n.toModel())
		}
	}
	if lc.PageInfo.HasPreviousPage && reviewID != "" {
		page.Next = append(page.Next, model.CommentQuery{
			ID: page.Query.ID, Since: page.Query.Since, ReviewID: reviewID, Before: lc.PageInfo.StartCursor,
		})
	}
}

func (x *commentsNode) toPage(q model.CommentQuery) model.CommentPage {
	page := model.CommentPage{Query: q, Found: true, Comments: []model.Comment{}}
	if q.ReviewID != "" {
		appendLineComments(&page, q.ReviewID, &x.Comments)
		return page
	}

	for _, item := range x.TimelineItems.Nodes {
		if item == nil {
			continue
		}
		switch item.TypeName {
		case "IssueComment":
			page.Comments = append(page.Comments, item.toModel())
		case "PullRequestReview":
			page.Comments = append(page.Comments, item.toModel())
			appendLineComments(&page, item.ID, &item.Comments)
		}
	}
	if pi := x.TimelineItems.PageInfo; pi.HasPreviousPage {
		page.Next = append(page.Next, model.CommentQuery{ID: q.ID, Since: q.Since, Before: pi.StartCursor})
	}
	return page
}

func (c *Client) ListComments(ctx context.Context, queries []model.CommentQuery) (*model.CommentsResult, error) {
	if len(queries) > maxCommentQueries {
		return nil, goerr.New("too many comment queries", goerr.V("count", len(queries)), goerr.V("max", maxCommentQueries))
	}
	result := &model.CommentsResult{RateLimit: model.RateLimit{Remaining: -1}}
	if len(queries) == 0 {
		return result, nil
	}

	query, vars := buildCommentsQuery(queries)
	var data map[string]json.RawMessage
	if err := c.graphql(ctx, query, vars, &data); err != nil {
		return nil, goerr.Wrap(err, "failed to list comments", goerr.V("count", len(queries)))
	}

	if raw, ok := data["rateLimit"]; ok {
		var rl *rateLimitNode
		if err := json.Unmarshal(raw, &rl); err != nil {
			return nil, goerr.Wrap(err, "failed to decode the rate limit")
		}
		result.RateLimit = rl.toModel()
	}

	result.Pages = make([]model.CommentPage, len(queries))
	for i, q := range queries {
		result.Pages[i] = model.CommentPage{Query: q}
		raw, ok := data[fmt.Sprintf("p%d", i)]
		if !ok {
			continue
		}
		var node *commentsNode
		if err := json.Unmarshal(raw, &node); err != nil {
			return nil, goerr.Wrap(err, "failed to decode comments", goerr.V("item_id", q.ID), goerr.V("review_id", q.ReviewID))
		}
		if node != nil {
			result.Pages[i] = node.toPage(q)
		}
	}
	return result, nil
}
