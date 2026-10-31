package model_test

import (
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

func TestLatestAddressedAt(t *testing.T) {
	approvedAt := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return approvedAt.Add(time.Duration(minutes) * time.Minute) }
	comment := func(author, body string, minutes int) model.Comment {
		return model.Comment{Author: author, Body: body, CreatedAt: at(minutes)}
	}

	t.Run("a mention or a reply addressed to the viewer", func(t *testing.T) {
		cases := map[string]model.Comment{
			"mention":                      comment("bob", "@alice could you check again?", 1),
			"mention in a sentence":        comment("bob", "Thanks, @alice.", 1),
			"mention in another case":      comment("bob", "@Alice please look", 1),
			"mention after a line break":   comment("bob", "fixed\n@alice", 1),
			"mention in parentheses":       comment("bob", "done (@alice)", 1),
			"reply to a line comment":      {Author: "bob", Body: "Fixed.", CreatedAt: at(1), ReplyToAuthor: "alice"},
			"reply in another login case":  {Author: "bob", Body: "Fixed.", CreatedAt: at(1), ReplyToAuthor: "ALICE"},
			"mention by a deleted account": comment("ghost", "@alice", 1),
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				gt.Equal(t, model.LatestAddressedAt("alice", approvedAt, []model.Comment{c}), at(1))
			})
		}
	})

	t.Run("a comment not addressed to the viewer", func(t *testing.T) {
		cases := map[string]model.Comment{
			"no mention":                  comment("bob", "LGTM", 1),
			"longer login":                comment("bob", "@alice-bot ping", 1),
			"login with a suffix":         comment("bob", "@alicesmith ping", 1),
			"e-mail address":              comment("bob", "mail bob@alice please", 1),
			"team of the same name":       comment("bob", "@alice/reviewers please", 1),
			"written by the viewer":       comment("alice", "@alice note to self", 1),
			"reply to another reviewer":   {Author: "bob", Body: "Fixed.", CreatedAt: at(1), ReplyToAuthor: "carol"},
			"reply by the viewer":         {Author: "alice", Body: "ok", CreatedAt: at(1), ReplyToAuthor: "alice"},
			"mention at the approval":     comment("bob", "@alice", 0),
			"mention before the approval": comment("bob", "@alice", -1),
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				gt.True(t, model.LatestAddressedAt("alice", approvedAt, []model.Comment{c}).IsZero())
			})
		}
	})

	t.Run("the latest of several", func(t *testing.T) {
		comments := []model.Comment{
			comment("bob", "@alice first", 3),
			comment("bob", "LGTM", 9),
			comment("carol", "@alice second", 5),
		}
		gt.Equal(t, model.LatestAddressedAt("alice", approvedAt, comments), at(5))
	})

	t.Run("no comments", func(t *testing.T) {
		gt.True(t, model.LatestAddressedAt("alice", approvedAt, nil).IsZero())
	})
}
