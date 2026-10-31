package model

import (
	"strconv"
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

// FilterScope selects which stored items the inbox screen lists.
type FilterScope string

const (
	// FilterScopeInbox lists the items for which Entry.InInbox is true.
	FilterScopeInbox FilterScope = "inbox"
	// FilterScopeAll also lists archived items and pull requests whose
	// review by the viewer is done.
	FilterScopeAll FilterScope = "all"
)

func (x FilterScope) Validate() error {
	switch x {
	case FilterScopeInbox, FilterScopeAll:
		return nil
	}
	return goerr.New("invalid filter scope", goerr.V("scope", x))
}

// Filter selects the entries shown in the inbox.
type Filter struct {
	Terms []string // lower-case, non-empty
	Scope FilterScope
}

// NewFilter splits keyword into lower-case terms separated by white space.
func NewFilter(keyword string, scope FilterScope) Filter {
	return Filter{Terms: strings.Fields(strings.ToLower(keyword)), Scope: scope}
}

// Match reports whether e belongs to at least one category, the inbox lists
// it (Entry.InInbox) unless the scope is FilterScopeAll, and its text
// contains every term. A scope other than FilterScopeAll, invalid ones
// included, is treated as the inbox so that it never lists more.
func (x Filter) Match(e *Entry) bool {
	if len(e.Categories) == 0 || (x.Scope != FilterScopeAll && !e.InInbox()) {
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
