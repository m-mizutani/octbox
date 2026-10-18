package model_test

import (
	"fmt"
	"testing"

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
	gt.A(t, model.NewFilter("Retry  API").Terms).Equal([]string{"retry", "api"})
	gt.A(t, model.NewFilter("   ").Terms).Length(0)
}

func TestFilterMatch(t *testing.T) {
	pr := validPR() // "Add retry", example-org/api#482, alice
	issue := validIssue()

	t.Run("every term must match", func(t *testing.T) {
		f := model.NewFilter("Retry  API")
		gt.True(t, f.Match(entry(pr)))

		other := pr
		other.Repo.Name = "web"
		gt.False(t, f.Match(entry(other)))
	})

	t.Run("number", func(t *testing.T) {
		gt.True(t, model.NewFilter("#482").Match(entry(pr)))
		gt.False(t, model.NewFilter("#483").Match(entry(pr)))
	})

	t.Run("author with and without @", func(t *testing.T) {
		gt.True(t, model.NewFilter("@alice").Match(entry(pr)))
		gt.True(t, model.NewFilter("alice").Match(entry(pr)))
		gt.False(t, model.NewFilter("@bob").Match(entry(pr)))
	})

	t.Run("kind word", func(t *testing.T) {
		gt.True(t, model.NewFilter("pr").Match(entry(pr)))
		gt.False(t, model.NewFilter("pr").Match(entry(issue)))
		gt.True(t, model.NewFilter("issue").Match(entry(issue)))
	})

	t.Run("empty filter matches by category only", func(t *testing.T) {
		gt.True(t, model.NewFilter("").Match(entry(pr)))
	})

	t.Run("no category never matches", func(t *testing.T) {
		e := &model.Entry{Item: pr}
		gt.False(t, model.NewFilter("").Match(e))
		gt.False(t, model.NewFilter("retry").Match(e))
	})
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
	f := model.NewFilter("webhook 99")

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
