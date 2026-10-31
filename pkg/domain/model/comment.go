package model

import (
	"regexp"
	"strings"
	"time"
)

// Comment is one comment on a pull request: a comment on the conversation,
// the body of a review, or a comment on a line of the diff.
type Comment struct {
	Author    string // login; "ghost" when the account is deleted
	Body      string
	CreatedAt time.Time // UTC
	// ReplyToAuthor is the login of the author of the line comment this
	// comment replies to; "" when it is not a reply.
	ReplyToAuthor string
}

// CommentQuery asks for one page of the comments of the pull request ID:
// the conversation comments and reviews updated at or after Since when
// ReviewID is empty, or the line comments of the review ReviewID otherwise.
// Before is the cursor of the page; empty for the latest page.
type CommentQuery struct {
	ID       ItemID
	Since    time.Time
	ReviewID string
	Before   string
}

// CommentPage is the answer to one CommentQuery.
type CommentPage struct {
	Query CommentQuery
	// Found is false when GitHub returned the node as null (deleted, or no
	// longer visible to the token); Comments and Next are then empty.
	Found    bool
	Comments []Comment
	// Next asks for the older pages: the previous page of the same query,
	// and the line comments of reviews that have more than were returned.
	Next []CommentQuery
}

// LatestAddressedAt returns when the latest comment addressed to viewer was
// written after since, or zero when there is none. A comment is addressed to
// viewer when someone else wrote it and it mentions @viewer, or it replies to
// a line comment of viewer. Logins are compared case-insensitively, as
// GitHub does.
func LatestAddressedAt(viewer string, since time.Time, comments []Comment) time.Time {
	// A login is letters, digits and hyphens. @viewer must not be part of an
	// e-mail address, a longer login, or a team (@viewer/team).
	mention := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_@./-])@` + regexp.QuoteMeta(viewer) + `($|[^A-Za-z0-9_/-])`)

	var latest time.Time
	for _, c := range comments {
		if !c.CreatedAt.After(since) || equalLogin(c.Author, viewer) {
			continue
		}
		if !equalLogin(c.ReplyToAuthor, viewer) && !mention.MatchString(c.Body) {
			continue
		}
		if c.CreatedAt.After(latest) {
			latest = c.CreatedAt
		}
	}
	return latest
}

func equalLogin(a, b string) bool {
	return a != "" && strings.EqualFold(a, b)
}
