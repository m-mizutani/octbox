package model_test

import (
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

func validPR() model.Item {
	return model.Item{
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
		UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
}

func validIssue() model.Item {
	return model.Item{
		ID:        "I_1",
		Kind:      model.ItemKindIssue,
		Repo:      model.RepoName{Owner: "octocat", Name: "hello-world"},
		Number:    77,
		Title:     "Crash when the config file is empty",
		URL:       "https://github.com/octocat/hello-world/issues/77",
		Author:    "carol",
		State:     model.ItemStateOpen,
		CI:        model.CIStateNone,
		Review:    model.ReviewStateNone,
		UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestItemValidate(t *testing.T) {
	t.Run("valid items", func(t *testing.T) {
		pr := validPR()
		gt.NoError(t, pr.Validate())
		issue := validIssue()
		gt.NoError(t, issue.Validate())
		issue.Author = ""
		gt.NoError(t, issue.Validate())
	})

	cases := map[string]func(x *model.Item){
		"empty id":          func(x *model.Item) { x.ID = "" },
		"empty owner":       func(x *model.Item) { x.Repo.Owner = "" },
		"empty name":        func(x *model.Item) { x.Repo.Name = "" },
		"zero number":       func(x *model.Item) { x.Number = 0 },
		"negative number":   func(x *model.Item) { x.Number = -1 },
		"empty title":       func(x *model.Item) { x.Title = "" },
		"empty url":         func(x *model.Item) { x.URL = "" },
		"non github url":    func(x *model.Item) { x.URL = "https://example.com/a" },
		"zero updated at":   func(x *model.Item) { x.UpdatedAt = time.Time{} },
		"unknown kind":      func(x *model.Item) { x.Kind = "discussion" },
		"unknown state":     func(x *model.Item) { x.State = "draft" },
		"unknown ci":        func(x *model.Item) { x.CI = "skipped" },
		"unknown review":    func(x *model.Item) { x.Review = "commented" },
		"merged issue":      func(x *model.Item) { x.State = model.ItemStateMerged },
		"draft issue":       func(x *model.Item) { x.IsDraft = true },
		"issue with ci":     func(x *model.Item) { x.CI = model.CIStateSuccess },
		"issue with review": func(x *model.Item) { x.Review = model.ReviewStateApproved },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			item := validIssue()
			mutate(&item)
			gt.Error(t, item.Validate())
		})
	}
}

func TestEntryUnread(t *testing.T) {
	updated := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	item := validPR()
	item.UpdatedAt = updated

	gt.True(t, (&model.Entry{Item: item}).Unread())
	gt.False(t, (&model.Entry{Item: item, ReadUpdatedAt: updated}).Unread())
	gt.True(t, (&model.Entry{Item: item, ReadUpdatedAt: updated.Add(-time.Second)}).Unread())
}

func TestRepoNameString(t *testing.T) {
	gt.Equal(t, model.RepoName{Owner: "o", Name: "r"}.String(), "o/r")
}
