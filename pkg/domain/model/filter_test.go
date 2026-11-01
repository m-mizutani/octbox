package model_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

func entry(item model.Item, categories ...model.Category) *model.Entry {
	if len(categories) == 0 {
		categories = []model.Category{model.CategoryCreated}
	}
	return &model.Entry{Item: item, Categories: categories}
}

func TestNewFilter(t *testing.T) {
	gt.A(t, model.NewFilter("Retry  API", inbox).Terms).Equal([]string{"retry", "api"})
	gt.A(t, model.NewFilter("   ", inbox).Terms).Length(0)
	gt.Equal(t, model.NewFilter("", model.FilterScopeAll).Scope, model.FilterScopeAll)
	gt.Equal(t, model.NewFilter("x", inbox).Scope, inbox)
}

const inbox = model.FilterScopeInbox

func TestFilterMatch(t *testing.T) {
	pr := validPR() // "Add retry", example-org/api#482, alice
	issue := validIssue()

	t.Run("every term must match", func(t *testing.T) {
		f := model.NewFilter("Retry  API", inbox)
		gt.True(t, f.Match(entry(pr)))

		other := pr
		other.Repo.Name = "web"
		gt.False(t, f.Match(entry(other)))
	})

	t.Run("number", func(t *testing.T) {
		gt.True(t, model.NewFilter("#482", inbox).Match(entry(pr)))
		gt.False(t, model.NewFilter("#483", inbox).Match(entry(pr)))
	})

	t.Run("author with and without @", func(t *testing.T) {
		gt.True(t, model.NewFilter("@alice", inbox).Match(entry(pr)))
		gt.True(t, model.NewFilter("alice", inbox).Match(entry(pr)))
		gt.False(t, model.NewFilter("@bob", inbox).Match(entry(pr)))
	})

	t.Run("kind word", func(t *testing.T) {
		gt.True(t, model.NewFilter("pr", inbox).Match(entry(pr)))
		gt.False(t, model.NewFilter("pr", inbox).Match(entry(issue)))
		gt.True(t, model.NewFilter("issue", inbox).Match(entry(issue)))
	})

	t.Run("empty filter matches by category only", func(t *testing.T) {
		gt.True(t, model.NewFilter("", inbox).Match(entry(pr)))
	})

	t.Run("pull request approved by the viewer never matches the inbox", func(t *testing.T) {
		approved := pr
		approved.ViewerApprovedAt = approved.UpdatedAt
		gt.False(t, model.NewFilter("", inbox).Match(entry(approved, model.CategoryReview)))
		gt.False(t, model.NewFilter("retry", inbox).Match(entry(approved, model.CategoryReview, model.CategoryAssigned)))

		approved.ViewerReviewRequested = true
		gt.True(t, model.NewFilter("", inbox).Match(entry(approved, model.CategoryReview)))
	})

	t.Run("no category never matches", func(t *testing.T) {
		e := &model.Entry{Item: pr}
		gt.False(t, model.NewFilter("", model.FilterScopeInbox).Match(e))
		gt.False(t, model.NewFilter("retry", model.FilterScopeInbox).Match(e))
		gt.False(t, model.NewFilter("", model.FilterScopeAll).Match(e))
	})

	t.Run("archived item matches only the all scope", func(t *testing.T) {
		archived := entry(pr)
		archived.ArchivedUpdatedAt = pr.UpdatedAt
		gt.False(t, model.NewFilter("", model.FilterScopeInbox).Match(archived))
		gt.True(t, model.NewFilter("", model.FilterScopeAll).Match(archived))
		gt.True(t, model.NewFilter("retry", model.FilterScopeAll).Match(archived))
		gt.False(t, model.NewFilter("webhook-x", model.FilterScopeAll).Match(archived))
	})

	t.Run("item updated after the archive matches the inbox", func(t *testing.T) {
		updated := entry(pr)
		updated.ArchivedUpdatedAt = pr.UpdatedAt.Add(-time.Minute)
		gt.True(t, model.NewFilter("", model.FilterScopeInbox).Match(updated))
	})

	t.Run("pull request approved by the viewer matches the all scope", func(t *testing.T) {
		approved := pr
		approved.ViewerApprovedAt = approved.UpdatedAt
		gt.True(t, model.NewFilter("retry", model.FilterScopeAll).Match(entry(approved, model.CategoryReview)))
	})

	t.Run("invalid scope is treated as the inbox", func(t *testing.T) {
		archived := entry(pr)
		archived.ArchivedUpdatedAt = pr.UpdatedAt
		gt.False(t, model.NewFilter("", model.FilterScope("")).Match(archived))
		gt.True(t, model.NewFilter("", model.FilterScope("")).Match(entry(pr)))
	})
}

func TestFilterScopeValidate(t *testing.T) {
	gt.NoError(t, model.FilterScopeInbox.Validate())
	gt.NoError(t, model.FilterScopeAll.Validate())
	gt.Error(t, model.FilterScope("").Validate())
	gt.Error(t, model.FilterScope("archive").Validate())
}

// BenchmarkFilter1000 measures one keystroke of filtering over 1,000 items;
// the requirement is 50 ms including rendering.
func BenchmarkFilter1000(b *testing.B) {
	entries := make([]*model.Entry, 1000)
	for i := range entries {
		item := validPR()
		item.ID = model.ItemID(fmt.Sprintf("PR_%d", i))
		item.Number = i + 1
		item.Title = fmt.Sprintf("Change number %d of the webhook sender", i)
		entries[i] = entry(item)
	}
	f := model.NewFilter("webhook 99", inbox)

	b.ResetTimer()
	for b.Loop() {
		n := 0
		for _, e := range entries {
			if f.Match(e) {
				n++
			}
		}
		_ = n
	}
}
