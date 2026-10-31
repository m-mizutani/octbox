package model

import (
	"strconv"
	"strings"
)

// Filter selects the entries shown in the inbox.
type Filter struct {
	Terms []string // lower-case, non-empty
}

// NewFilter splits keyword into lower-case terms separated by white space.
func NewFilter(keyword string) Filter {
	return Filter{Terms: strings.Fields(strings.ToLower(keyword))}
}

// Match reports whether e belongs to at least one category, the viewer's
// review of it is not done (Item.ViewerReviewDone), and its text contains
// every term.
func (x Filter) Match(e *Entry) bool {
	if len(e.Categories) == 0 || e.Item.ViewerReviewDone() {
		return false
	}
	if len(x.Terms) == 0 {
		return true
	}

	haystack := strings.ToLower(matchText(&e.Item))
	for _, term := range x.Terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func matchText(item *Item) string {
	kindWord := "issue"
	if item.Kind == ItemKindPullRequest {
		kindWord = "pr"
	}
	return item.Title + " " + item.Repo.String() + "#" + strconv.Itoa(item.Number) +
		" @" + item.Author + " " + kindWord
}
