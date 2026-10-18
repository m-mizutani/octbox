package model

import (
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// ItemID is the GraphQL node ID of an issue or a pull request, e.g.
// "PR_kwDOAbc123".
type ItemID string

type ItemKind string

const (
	ItemKindIssue       ItemKind = "issue"
	ItemKindPullRequest ItemKind = "pull_request"
)

// ItemState is the state GitHub reports. Only open items are stored; the
// syncer deletes an item when it sees it closed or merged.
type ItemState string

const (
	ItemStateOpen   ItemState = "open"
	ItemStateClosed ItemState = "closed"
	ItemStateMerged ItemState = "merged" // pull requests only
)

type CIState string

const (
	CIStateNone    CIState = "none"
	CIStateSuccess CIState = "success" // SUCCESS
	CIStateFailure CIState = "failure" // FAILURE, ERROR
	CIStatePending CIState = "pending" // PENDING, EXPECTED
)

type ReviewState string

const (
	ReviewStateNone             ReviewState = "none"
	ReviewStateApproved         ReviewState = "approved"
	ReviewStateChangesRequested ReviewState = "changes_requested"
	ReviewStateRequired         ReviewState = "review_required"
)

type Category string

const (
	CategoryReview     Category = "review"
	CategoryAssigned   Category = "assigned"
	CategoryCreated    Category = "created"
	CategoryCommented  Category = "commented"
	CategorySubscribed Category = "subscribed"
)

// Categories is the order of Entry.Categories.
var Categories = []Category{CategoryReview, CategoryAssigned, CategoryCreated, CategoryCommented, CategorySubscribed}

type RepoName struct {
	Owner string
	Name  string
}

// String returns "owner/name".
func (x RepoName) String() string {
	return x.Owner + "/" + x.Name
}

type Item struct {
	ID        ItemID
	Kind      ItemKind
	Repo      RepoName
	Number    int
	Title     string
	URL       string // https://github.com/{owner}/{name}/(issues|pull)/{number}
	Author    string // login; "ghost" when the account is deleted
	State     ItemState
	IsDraft   bool        // false for issues
	CI        CIState     // CIStateNone for issues
	Review    ReviewState // ReviewStateNone for issues
	UpdatedAt time.Time   // GitHub updatedAt, UTC
}

// Ref returns the repository and number of the item.
func (x *Item) Ref() ItemRef {
	return ItemRef{Repo: x.Repo, Number: x.Number}
}

func (x *Item) Validate() error {
	invalid := func(field string) error {
		return goerr.New("invalid item", goerr.V("item_id", x.ID), goerr.V("field", field))
	}

	switch {
	case x.ID == "":
		return invalid("id")
	case x.Repo.Owner == "":
		return invalid("repo.owner")
	case x.Repo.Name == "":
		return invalid("repo.name")
	case x.Number <= 0:
		return invalid("number")
	case x.Title == "":
		return invalid("title")
	case x.URL == "":
		return invalid("url")
	case !strings.HasPrefix(x.URL, "https://github.com/"):
		return invalid("url")
	case x.UpdatedAt.IsZero():
		return invalid("updated_at")
	}

	switch x.Kind {
	case ItemKindIssue, ItemKindPullRequest:
	default:
		return invalid("kind")
	}
	switch x.State {
	case ItemStateOpen, ItemStateClosed, ItemStateMerged:
	default:
		return invalid("state")
	}
	switch x.CI {
	case CIStateNone, CIStateSuccess, CIStateFailure, CIStatePending:
	default:
		return invalid("ci")
	}
	switch x.Review {
	case ReviewStateNone, ReviewStateApproved, ReviewStateChangesRequested, ReviewStateRequired:
	default:
		return invalid("review")
	}

	if x.Kind == ItemKindIssue {
		switch {
		case x.State == ItemStateMerged:
			return invalid("state")
		case x.IsDraft:
			return invalid("is_draft")
		case x.CI != CIStateNone:
			return invalid("ci")
		case x.Review != ReviewStateNone:
			return invalid("review")
		}
	}
	return nil
}

type ItemRef struct {
	Repo   RepoName
	Number int
}

type Entry struct {
	Item          Item
	Categories    []Category // order of Categories
	ReadUpdatedAt time.Time  // zero when never read
}

// Unread reports whether the item was updated after the user last read it.
func (x *Entry) Unread() bool {
	return x.ReadUpdatedAt.Before(x.Item.UpdatedAt)
}
