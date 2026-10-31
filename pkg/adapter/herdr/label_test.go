package herdr_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/herdr"
)

// layout is the state of a fake herdr: which tab each pane is in, and the
// names of tabs and panes. A pane without a name is absent from paneLabels.
type layout struct {
	mu         sync.Mutex
	paneTab    map[string]string
	tabLabels  map[string]string
	paneLabels map[string]string
}

func (l *layout) reply(req received) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var p struct {
		PaneID string  `json:"pane_id"`
		TabID  string  `json:"tab_id"`
		Label  *string `json:"label"`
	}
	_ = json.Unmarshal(req.Params, &p)
	result := func(v any) string {
		raw, _ := json.Marshal(map[string]any{"id": req.ID, "result": v})
		return string(raw)
	}
	notFound := func(code string) string {
		return `{"id":"` + req.ID + `","error":{"code":"` + code + `","message":"not found"}}`
	}

	switch req.Method {
	case "pane.get":
		tabID, ok := l.paneTab[p.PaneID]
		if !ok {
			return notFound("pane_not_found")
		}
		pane := map[string]any{"pane_id": p.PaneID, "tab_id": tabID}
		if label, ok := l.paneLabels[p.PaneID]; ok {
			pane["label"] = label
		}
		return result(map[string]any{"type": "pane_info", "pane": pane})
	case "tab.get":
		label, ok := l.tabLabels[p.TabID]
		if !ok {
			return notFound("tab_not_found")
		}
		count := 0
		for _, tabID := range l.paneTab {
			if tabID == p.TabID {
				count++
			}
		}
		return result(map[string]any{"type": "tab_info", "tab": map[string]any{
			"tab_id": p.TabID, "label": label, "pane_count": count,
		}})
	case "tab.rename":
		if _, ok := l.tabLabels[p.TabID]; !ok {
			return notFound("tab_not_found")
		}
		l.tabLabels[p.TabID] = *p.Label
		return result(map[string]any{"type": "tab_info"})
	case "pane.rename":
		if p.Label == nil {
			delete(l.paneLabels, p.PaneID)
		} else {
			l.paneLabels[p.PaneID] = *p.Label
		}
		return result(map[string]any{"type": "pane_info"})
	}
	return notFound("unknown_method")
}

func (l *layout) names() (map[string]string, map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	tabs, panes := map[string]string{}, map[string]string{}
	for k, v := range l.tabLabels {
		tabs[k] = v
	}
	for k, v := range l.paneLabels {
		panes[k] = v
	}
	return tabs, panes
}

func (l *layout) update(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f()
}

func renames(f *fakeHerdr) int {
	n := 0
	for _, req := range f.received() {
		if req.Method == "tab.rename" || req.Method == "pane.rename" {
			n++
		}
	}
	return n
}

func TestUnreadCount(t *testing.T) {
	ctx := context.Background()

	// newLayout returns w1:p1 alone in tab w1:t1 ("work"), and tab w1:t2
	// ("2", its automatic name).
	newLayout := func() *layout {
		return &layout{
			paneTab:    map[string]string{"w1:p1": "w1:t1"},
			tabLabels:  map[string]string{"w1:t1": "work", "w1:t2": "2"},
			paneLabels: map[string]string{},
		}
	}

	t.Run("the only pane in its tab names the tab", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")

		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, panes := l.names()
		gt.Equal(t, tabs["w1:t1"], "[octbox] 2 unread")
		gt.Equal(t, panes, map[string]string{})

		gt.NoError(t, client.ShowUnreadCount(ctx, 0)).Required()
		tabs, _ = l.names()
		gt.Equal(t, tabs["w1:t1"], "[octbox] 0 unread")

		gt.NoError(t, client.RemoveUnreadCount(ctx)).Required()
		tabs, _ = l.names()
		gt.Equal(t, tabs["w1:t1"], "work")
	})

	t.Run("a pane sharing its tab names the pane and leaves the tab", func(t *testing.T) {
		l := newLayout()
		l.paneTab["w1:p2"] = "w1:t1"
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")

		gt.NoError(t, client.ShowUnreadCount(ctx, 3)).Required()
		tabs, panes := l.names()
		gt.Equal(t, tabs["w1:t1"], "work")
		gt.Equal(t, panes, map[string]string{"w1:p1": "[octbox] 3 unread"})

		// The pane had no name, so removing clears it.
		gt.NoError(t, client.RemoveUnreadCount(ctx)).Required()
		_, panes = l.names()
		gt.Equal(t, panes, map[string]string{})
	})

	t.Run("a pane name set before is put back", func(t *testing.T) {
		l := newLayout()
		l.paneTab["w1:p2"] = "w1:t1"
		l.paneLabels["w1:p1"] = "inbox"
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")

		gt.NoError(t, client.ShowUnreadCount(ctx, 1)).Required()
		gt.NoError(t, client.RemoveUnreadCount(ctx)).Required()
		_, panes := l.names()
		gt.Equal(t, panes, map[string]string{"w1:p1": "inbox"})
	})

	t.Run("an unchanged count sends no rename", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")

		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		gt.Equal(t, renames(f), 1)
	})

	t.Run("splitting the tab moves the count to the pane", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()

		l.update(func() { l.paneTab["w1:p2"] = "w1:t1" })
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, panes := l.names()
		gt.Equal(t, tabs["w1:t1"], "work")
		gt.Equal(t, panes, map[string]string{"w1:p1": "[octbox] 2 unread"})

		// Closing the other pane moves it back.
		l.update(func() { delete(l.paneTab, "w1:p2") })
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, panes = l.names()
		gt.Equal(t, tabs["w1:t1"], "[octbox] 2 unread")
		gt.Equal(t, panes, map[string]string{})
	})

	t.Run("moving the pane to another tab moves the count", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()

		l.update(func() { l.paneTab["w1:p1"] = "w1:t2" })
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, _ := l.names()
		gt.Equal(t, tabs, map[string]string{"w1:t1": "work", "w1:t2": "[octbox] 2 unread"})

		// The automatic name is put back as the same text.
		gt.NoError(t, client.RemoveUnreadCount(ctx)).Required()
		tabs, _ = l.names()
		gt.Equal(t, tabs["w1:t2"], "2")
	})

	t.Run("a closed tab needs nothing put back", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()

		// The pane moved out of w1:t1, which closed as it became empty.
		l.update(func() {
			l.paneTab["w1:p1"] = "w1:t2"
			delete(l.tabLabels, "w1:t1")
		})
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, _ := l.names()
		gt.Equal(t, tabs, map[string]string{"w1:t2": "[octbox] 2 unread"})
	})

	t.Run("a name the user set is kept until the count changes", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		client := herdr.New(f.path, "w1:p1")
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()

		l.update(func() { l.tabLabels["w1:t1"] = "mine" })
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, _ := l.names()
		gt.Equal(t, tabs["w1:t1"], "mine")

		gt.NoError(t, client.ShowUnreadCount(ctx, 1)).Required()
		tabs, _ = l.names()
		gt.Equal(t, tabs["w1:t1"], "[octbox] 1 unread")
	})

	t.Run("RemoveUnreadCount without a shown count sends nothing", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		gt.NoError(t, herdr.New(f.path, "w1:p1").RemoveUnreadCount(ctx)).Required()
		gt.A(t, f.received()).Length(0)
	})

	t.Run("a pane herdr does not know is an error", func(t *testing.T) {
		l := newLayout()
		f := newFakeHerdr(t, l.reply)
		gt.Error(t, herdr.New(f.path, "w9:p9").ShowUnreadCount(ctx, 1))
		gt.Equal(t, renames(f), 0)
	})

	t.Run("a failed rename is retried at the next call", func(t *testing.T) {
		l := newLayout()
		var failRename atomic.Bool
		failRename.Store(true)
		f := newFakeHerdr(t, func(req received) string {
			if req.Method == "tab.rename" && failRename.Load() {
				return `{"id":"` + req.ID + `","error":{"code":"internal","message":"failed"}}`
			}
			return l.reply(req)
		})
		client := herdr.New(f.path, "w1:p1")
		gt.Error(t, client.ShowUnreadCount(ctx, 2))

		failRename.Store(false)
		gt.NoError(t, client.ShowUnreadCount(ctx, 2)).Required()
		tabs, _ := l.names()
		gt.Equal(t, tabs["w1:t1"], "[octbox] 2 unread")

		gt.NoError(t, client.RemoveUnreadCount(ctx)).Required()
		tabs, _ = l.names()
		gt.Equal(t, tabs["w1:t1"], "work")
	})
}
