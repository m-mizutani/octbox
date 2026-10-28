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

// Page and batch sizes are kept small: GitHub answers 502 when a query runs
// past its time limit, and the CI rollup and reviews of each pull request
// make every node expensive.
const (
	maxGetItems     = 50
	maxResolveItems = 50
)

const itemFieldsFragment = `
fragment ItemFields on Node {
  __typename
  id
  ... on Issue {
    number title url updatedAt state
    author { login }
    repository { owner { login } name }
  }
  ... on PullRequest {
    number title url updatedAt state isDraft reviewDecision
    author { login }
    repository { owner { login } name }
    latestReviews(first: 10) { nodes { state } }
    commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  }
}`

const searchQuery = `
query Search($q: String!, $after: String) {
  rateLimit { remaining resetAt }
  search(query: $q, type: ISSUE, first: 25, after: $after) {
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
		Name string `json:"name"`
	} `json:"repository"`
	LatestReviews struct {
		Nodes []struct {
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
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
		ID:        model.ItemID(x.ID),
		Kind:      kind,
		Repo:      model.RepoName{Owner: x.Repository.Owner.Login, Name: x.Repository.Name},
		Number:    x.Number,
		Title:     x.Title,
		URL:       x.URL,
		Author:    "ghost",
		CI:        model.CIStateNone,
		Review:    model.ReviewStateNone,
		UpdatedAt: x.UpdatedAt.UTC(),
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
	vars := map[string]any{"q": query, "after": nil}
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
