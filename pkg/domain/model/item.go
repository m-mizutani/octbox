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

// ItemState is the state GitHub reports. Only open items of repositories that
// are not archived are stored; the syncer deletes an item when it sees it
// closed, merged, or in an archived repository.
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
	// ViewerApprovedAt is when the viewer submitted their latest review of
	// the pull request, when that review is an approval. It is zero when the
	// viewer has not reviewed it, or when the latest review is not an
	// approval (dismissed, a comment, or a change request). Zero for issues.
	ViewerApprovedAt time.Time
	// ViewerReviewRequested is true while a review of the pull request is
	// requested from the viewer or a team of the viewer. GitHub removes the
	// request when the viewer submits a review, and adds it again when the
	// author re-requests one. false for issues.
	ViewerReviewRequested bool
	// ViewerAddressedAt is when the latest comment addressed to the viewer
	// (see LatestAddressedAt) was written after ViewerApprovedAt. The GitHub
	// client leaves it zero; the syncer sets it from the comments of the pull
	// request. Zero when no such comment was found, and for issues.
	ViewerAddressedAt time.Time
	// RepoArchived is true when the repository is archived. Such items are
	// deleted like closed ones, so it is never stored.
	RepoArchived bool
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
		case !x.ViewerApprovedAt.IsZero():
			return invalid("viewer_approved_at")
		case x.ViewerReviewRequested:
			return invalid("viewer_review_requested")
		case !x.ViewerAddressedAt.IsZero():
			return invalid("viewer_addressed_at")
		}
	}
	return nil
}

// ViewerReviewDone reports whether the viewer has approved the pull request,
// no review is requested from the viewer again, and no comment addressed to
// the viewer was written after the approval. The inbox, the unread count,
// and the update indicator leave such items out.
func (x *Item) ViewerReviewDone() bool {
	return !x.ViewerApprovedAt.IsZero() && !x.ViewerReviewRequested &&
		!x.ViewerAddressedAt.After(x.ViewerApprovedAt)
}

type ItemRef struct {
	Repo   RepoName
	Number int
}

type Entry struct {
	Item          Item
	Categories    []Category // order of Categories
	ReadUpdatedAt time.Time  // zero when never read
	// ArchivedUpdatedAt is the Item.UpdatedAt shown to the user when they
	// archived the item. Zero when never archived. A later Item.UpdatedAt
	// means the item was updated after it was archived.
	ArchivedUpdatedAt time.Time
}

// Unread reports whether the item was updated after the user last read it.
func (x *Entry) Unread() bool {
	return x.ReadUpdatedAt.Before(x.Item.UpdatedAt)
}

// Archived reports whether the user archived the item and it has not been
// updated since.
func (x *Entry) Archived() bool {
	return !x.ArchivedUpdatedAt.IsZero() && !x.Item.UpdatedAt.After(x.ArchivedUpdatedAt)
}

// InInbox reports whether the inbox lists the item: it is not archived and
// the viewer's review of it is not done. The unread count and the update
// indicator use the same condition.
func (x *Entry) InInbox() bool {
	return !x.Archived() && !x.Item.ViewerReviewDone()
}
