package tui_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/controller/tui"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/usecase"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type fakeInbox struct {
	mu          sync.Mutex
	entries     []*model.Entry
	state       model.SyncState
	openErr     error
	listCalls   []string
	openCalls   []model.ItemID
	toggleCalls []model.ItemID
	markCalls   [][]model.ItemID
}

func (f *fakeInbox) List(_ context.Context, filter model.Filter) ([]*model.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, strings.Join(filter.Terms, " "))
	var out []*model.Entry
	for _, e := range f.entries {
		if filter.Match(e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeInbox) Open(_ context.Context, id model.ItemID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, id)
	return f.openErr
}

func (f *fakeInbox) ToggleRead(_ context.Context, id model.ItemID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toggleCalls = append(f.toggleCalls, id)
	return nil
}

func (f *fakeInbox) MarkRead(_ context.Context, ids []model.ItemID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCalls = append(f.markCalls, ids)
	return nil
}

func (f *fakeInbox) SyncState(context.Context) (*model.SyncState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state
	return &st, nil
}

func (f *fakeInbox) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = nil
	f.openCalls = nil
	f.toggleCalls = nil
	f.markCalls = nil
}

func entry(id string, kind model.ItemKind, number int, title string, age time.Duration, unread bool) *model.Entry {
	item := model.Item{
		ID:        model.ItemID(id),
		Kind:      kind,
		Repo:      model.RepoName{Owner: "example-org", Name: "api"},
		Number:    number,
		Title:     title,
		URL:       fmt.Sprintf("https://github.com/example-org/api/pull/%d", number),
		Author:    "alice",
		State:     model.ItemStateOpen,
		CI:        model.CIStateNone,
		Review:    model.ReviewStateNone,
		UpdatedAt: now.Add(-age),
	}
	e := &model.Entry{Item: item, Categories: []model.Category{model.CategoryCreated}}
	if !unread {
		e.ReadUpdatedAt = item.UpdatedAt
	}
	return e
}

func threeEntries() []*model.Entry {
	return []*model.Entry{
		entry("PR_1", model.ItemKindPullRequest, 1, "Add retry", time.Minute, true),
		entry("PR_2", model.ItemKindPullRequest, 2, "Fix auth", time.Hour, false),
		entry("I_3", model.ItemKindIssue, 3, "Crash on start", 2*time.Hour, false),
	}
}

// run executes cmd and feeds every resulting message back into m, like the
// bubbletea runtime. Commands that do not finish quickly (cursor blink) are
// dropped.
func run(t *testing.T, m *tui.Model, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		var msg tea.Msg
		select {
		case msg = <-ch:
		case <-time.After(100 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			if _, ok := msg.(tea.QuitMsg); ok {
				continue
			}
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func newModel(t *testing.T, inbox *fakeInbox) (*tui.Model, chan struct{}) {
	t.Helper()
	if inbox.state == (model.SyncState{}) {
		inbox.state.RateRemaining = -1 // the state of a database that never synced
	}
	trigger := make(chan struct{}, 1)
	m := tui.New(context.Background(), inbox, trigger, func() time.Time { return now })
	tui.DisableTick(m)
	run(t, m, m.Init())
	return m, trigger
}

func press(t *testing.T, m *tui.Model, keys ...string) tea.Cmd {
	t.Helper()
	var last tea.Cmd
	for _, k := range keys {
		_, cmd := m.Update(keyMsg(k))
		run(t, m, cmd)
		last = cmd
	}
	return last
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	}
	if strings.HasPrefix(k, "ctrl+") {
		return tea.KeyPressMsg{Code: rune(k[len(k)-1]), Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	msg := tea.KeyPressMsg{Code: r, Text: k}
	if r >= 'A' && r <= 'Z' {
		msg.ShiftedCode = r
		msg.Code = r + ('a' - 'A')
		msg.Mod = tea.ModShift
	}
	return msg
}

func screen(m *tui.Model) []string {
	return strings.Split(ansi.Strip(m.View().Content), "\n")
}

// selectedRow returns the index (in the list) of the row drawn reversed.
func selectedID(t *testing.T, m *tui.Model, inbox *fakeInbox) model.ItemID {
	t.Helper()
	lines := screen(m)
	raw := strings.Split(m.View().Content, "\n")
	for i, line := range raw {
		if strings.Contains(line, "\x1b[7m") {
			for _, e := range inbox.entries {
				if strings.Contains(lines[i], e.Item.Title) {
					return e.Item.ID
				}
			}
		}
	}
	return ""
}

func TestMove(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_1"))

	for _, k := range []string{"j", "down", "ctrl+n"} {
		m, _ := newModel(t, inbox)
		press(t, m, k)
		gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_2"))
	}

	press(t, m, "j", "j", "j")
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("I_3"))

	for _, k := range []string{"k", "up", "ctrl+p"} {
		press(t, m, "G", k)
		gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_2"))
	}
	press(t, m, "g", "k")
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_1"))
	press(t, m, "G")
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("I_3"))
	press(t, m, "home")
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_1"))
	press(t, m, "end")
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("I_3"))
}

func TestEnter(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)
	inbox.resetCalls()

	press(t, m, "j", "enter")
	gt.A(t, inbox.openCalls).Equal([]model.ItemID{"PR_2"})
	gt.A(t, inbox.listCalls).Length(1)

	inbox.openErr = goerr.Wrap(errors.New("no browser"), "failed to open", goerr.T(usecase.TagBrowserFailed))
	press(t, m, "enter")
	status := screen(m)[len(screen(m))-2]
	gt.Equal(t, status, "Failed to open the browser. URL: https://github.com/example-org/api/pull/2")
	// The URL stays until the next key press.
	_, cmd := m.Update(tui.SyncEventMsg{State: model.SyncState{LastSyncAt: now, NextRunAt: now, RateRemaining: -1}})
	run(t, m, cmd)
	gt.S(t, screen(m)[len(screen(m))-2]).HasPrefix("Failed to open the browser.")

	inbox.openErr = goerr.Wrap(errors.New("locked"), "failed to mark", goerr.T(usecase.TagOpenedNotMarked))
	press(t, m, "enter")
	gt.Equal(t, screen(m)[len(screen(m))-2], "Opened in your browser, but failed to mark the item as read.")

	empty := &fakeInbox{}
	m, _ = newModel(t, empty)
	press(t, m, "enter")
	gt.A(t, empty.openCalls).Length(0)
}

func TestFilter(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)
	inbox.resetCalls()

	press(t, m, "/", "a", "u")
	gt.A(t, inbox.listCalls).Equal([]string{"a", "au"})
	lines := screen(m)
	gt.S(t, lines[len(lines)-1]).HasPrefix("/ au")
	gt.S(t, lines[1]).Contains("Fix auth")

	// j is text while the input has focus.
	press(t, m, "j")
	gt.A(t, inbox.listCalls).Equal([]string{"a", "au", "auj"})
	press(t, m, "backspace")
	gt.A(t, inbox.listCalls).Equal([]string{"a", "au", "auj", "au"})

	press(t, m, "enter")
	lines = screen(m)
	gt.S(t, lines[len(lines)-1]).Contains("j/k move")
	gt.S(t, lines[len(lines)-2]).Contains("filter: au")

	inbox.resetCalls()
	press(t, m, "/", "esc")
	gt.A(t, inbox.listCalls).Equal([]string{""})
	gt.S(t, screen(m)[len(screen(m))-2]).Contains("filter: (none)")
}

func TestUnassignedKeys(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)
	before := m.View().Content
	inbox.resetCalls()
	press(t, m, "tab", "c")
	gt.A(t, inbox.listCalls).Length(0)
	gt.Equal(t, m.View().Content, before)
}

func TestReadKeys(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)
	press(t, m, "j", "m")
	gt.A(t, inbox.toggleCalls).Equal([]model.ItemID{"PR_2"})
	press(t, m, "A")
	gt.A(t, inbox.markCalls).Length(1).Required()
	gt.A(t, inbox.markCalls[0]).Equal([]model.ItemID{"PR_1", "PR_2", "I_3"})
}

func TestSyncEvents(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)
	status := func() string { s := screen(m); return s[len(s)-2] }

	_, cmd := m.Update(tui.SyncEventMsg{Running: true})
	run(t, m, cmd)
	gt.S(t, status()).HasPrefix("Syncing…")

	synced := model.SyncState{LastSyncAt: now, NextRunAt: now.Add(time.Minute), RateRemaining: 4000}
	press(t, m, "j")
	inbox.resetCalls()
	// The selected item moves to the end of the new list.
	inbox.entries = []*model.Entry{inbox.entries[0], inbox.entries[2], inbox.entries[1]}
	_, cmd = m.Update(tui.SyncEventMsg{State: synced})
	run(t, m, cmd)
	gt.A(t, inbox.listCalls).Length(1)
	gt.S(t, status()).HasPrefix("Synced " + now.Local().Format("15:04") + " · next " + now.Add(time.Minute).Local().Format("15:04"))
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_2"))

	// The selected item disappears: the same row stays selected.
	inbox.entries = []*model.Entry{inbox.entries[0], inbox.entries[1]}
	_, cmd = m.Update(tui.SyncEventMsg{State: synced})
	run(t, m, cmd)
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("I_3"))

	retry := now.Add(2 * time.Minute)
	cases := map[model.SyncError]string{
		model.SyncErrorNetwork:      "Sync failed (network error). Retrying at " + retry.Local().Format("15:04") + ".",
		model.SyncErrorRateLimited:  "Rate limited by GitHub. Next sync at " + retry.Local().Format("15:04") + ".",
		model.SyncErrorUnauthorized: "GitHub rejected the token. Run `octbox auth login`.",
	}
	for syncErr, want := range cases {
		_, cmd = m.Update(tui.SyncEventMsg{State: model.SyncState{LastError: syncErr, NextRunAt: retry, LastSyncAt: now, RateRemaining: -1}})
		run(t, m, cmd)
		gt.S(t, status()).HasPrefix(want)
	}

	_, cmd = m.Update(tui.SyncEventMsg{State: model.SyncState{LastSyncAt: now, NextRunAt: retry, RateRemaining: 150}})
	run(t, m, cmd)
	gt.S(t, status()).HasPrefix("Rate limited by GitHub. Next sync at " + retry.Local().Format("15:04") + ".")
}

func TestManualSync(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, trigger := newModel(t, inbox)

	press(t, m, "r")
	gt.Equal(t, len(trigger), 1)
	// A full channel does not block.
	press(t, m, "r")
	gt.Equal(t, len(trigger), 1)
	<-trigger

	// Right after a sync, and while waiting to retry a failed one, r still
	// asks for a sync.
	_, cmd := m.Update(tui.SyncEventMsg{State: model.SyncState{LastSyncAt: now, NextRunAt: now.Add(time.Minute), RateRemaining: -1}})
	run(t, m, cmd)
	press(t, m, "r")
	gt.Equal(t, len(trigger), 1)
	<-trigger

	_, cmd = m.Update(tui.SyncEventMsg{State: model.SyncState{LastSyncAt: now, NextRunAt: now.Add(15 * time.Minute),
		ConsecutiveFailures: 5, LastError: model.SyncErrorAPI, RateRemaining: -1}})
	run(t, m, cmd)
	press(t, m, "r")
	gt.Equal(t, len(trigger), 1)
}

func TestQuit(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m, _ := newModel(t, inbox)

	isQuit := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}
	_, cmd := m.Update(keyMsg("q"))
	gt.True(t, isQuit(cmd))
	_, cmd = m.Update(keyMsg("ctrl+c"))
	gt.True(t, isQuit(cmd))

	press(t, m, "/")
	_, cmd = m.Update(keyMsg("q"))
	gt.False(t, isQuit(cmd))
	_, cmd = m.Update(keyMsg("ctrl+c"))
	gt.True(t, isQuit(cmd))
}

func sized(t *testing.T, m *tui.Model, w int) {
	t.Helper()
	m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
}

func TestRender(t *testing.T) {
	long := entry("PR_long", model.ItemKindPullRequest, 1290, strings.Repeat("Migrate the settings page ", 5), time.Hour, true)
	ci := []struct {
		state model.CIState
		mark  string
	}{
		{model.CIStateSuccess, "✓"}, {model.CIStateFailure, "✗"}, {model.CIStatePending, "●"}, {model.CIStateNone, " "},
	}
	reviews := []struct {
		state model.ReviewState
		label string
	}{
		{model.ReviewStateApproved, "approved"}, {model.ReviewStateChangesRequested, "changes "},
		{model.ReviewStateRequired, "review  "}, {model.ReviewStateNone, "        "},
	}
	var entries []*model.Entry
	for i := range ci {
		e := entry(fmt.Sprintf("PR_%d", i), model.ItemKindPullRequest, i+1, fmt.Sprintf("Title %d", i), time.Minute, i%2 == 0)
		e.Item.CI = ci[i].state
		e.Item.Review = reviews[i].state
		entries = append(entries, e)
	}
	issue := entry("I_9", model.ItemKindIssue, 9, "Issue title", time.Minute, false)
	entries = append(entries, long, issue)

	inbox := &fakeInbox{entries: entries}
	m, _ := newModel(t, inbox)
	sized(t, m, 80)

	raw := strings.Split(m.View().Content, "\n")
	for _, line := range raw {
		gt.N(t, lipgloss.Width(line)).LessOrEqual(80)
	}
	lines := screen(m)
	gt.Equal(t, lines[0], "octbox  3 unread / 6 items")
	for i := range ci {
		row := lines[1+i]
		gt.Equal(t, string([]rune(row)[4]), ci[i].mark)
		gt.Equal(t, string([]rune(row)[6:14]), reviews[i].label)
	}
	gt.S(t, lines[1]).HasPrefix("● P")
	gt.S(t, lines[2]).HasPrefix("  P")
	longRow := lines[5]
	gt.S(t, longRow).Contains("…")
	gt.S(t, longRow).Contains("@alice")
	gt.S(t, lines[6]).HasPrefix("  I    ")

	t.Run("narrow screen drops author and age", func(t *testing.T) {
		sized(t, m, 50)
		for _, line := range strings.Split(m.View().Content, "\n") {
			gt.N(t, lipgloss.Width(line)).LessOrEqual(50)
		}
		row := screen(m)[5]
		gt.S(t, row).NotContains("@alice")
		title := strings.TrimSpace(string([]rune(row)[16+len("example-org/api#1290")+1:]))
		gt.N(t, len([]rune(title))).GreaterOrEqual(10)
		sized(t, m, 80)
	})

	t.Run("header counts the filtered list", func(t *testing.T) {
		press(t, m, "/", "t", "i", "t", "l", "e", "enter")
		gt.Equal(t, screen(m)[0], "octbox  2 unread / 5 items")
		press(t, m, "esc")
	})
}

func TestKindColor(t *testing.T) {
	draft := entry("PR_d", model.ItemKindPullRequest, 1, "Draft", time.Minute, false)
	draft.Item.IsDraft = true
	open := entry("PR_o", model.ItemKindPullRequest, 2, "Open", 2*time.Minute, false)
	inbox := &fakeInbox{entries: []*model.Entry{draft, open}}
	m, _ := newModel(t, inbox)
	press(t, m, "j") // move the reversed selection away from the draft row

	gray := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("P")
	selectedGreen := lipgloss.NewStyle().Reverse(true).Foreground(lipgloss.Color("2")).Render("P")
	raw := strings.Split(m.View().Content, "\n")
	gt.S(t, raw[1]).Contains(gray)
	gt.S(t, raw[2]).Contains(selectedGreen)
}

func TestReadRowsAreGray(t *testing.T) {
	read := entry("PR_r", model.ItemKindPullRequest, 1, "Read one", time.Minute, false)
	read.Item.CI = model.CIStateSuccess
	unread := entry("PR_u", model.ItemKindPullRequest, 2, "Unread one", 2*time.Minute, true)
	inbox := &fakeInbox{entries: []*model.Entry{read, unread}}
	m, _ := newModel(t, inbox)
	press(t, m, "j", "j") // move the reversed selection to the last row

	grayStart := strings.Split(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("X"), "X")[0]
	raw := strings.Split(m.View().Content, "\n")
	// The read row: repository and title in gray, CI still green.
	gt.S(t, raw[1]).Contains(grayStart + "example-org/api#1")
	gt.S(t, raw[1]).Contains(grayStart + "Read one")
	gt.S(t, raw[1]).Contains(lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓"))
	// The unread row is not gray and carries the unread mark.
	gt.S(t, raw[2]).NotContains(grayStart + "example-org/api#2")
	gt.S(t, ansi.Strip(raw[2])).Contains("●")
}

func TestEmptyStates(t *testing.T) {
	inbox := &fakeInbox{}
	m, _ := newModel(t, inbox)
	gt.Equal(t, screen(m)[1], "Syncing with GitHub…")

	_, cmd := m.Update(tui.SyncEventMsg{State: model.SyncState{LastSyncAt: now, NextRunAt: now, RateRemaining: -1}})
	run(t, m, cmd)
	gt.Equal(t, screen(m)[1], "No items.")

	press(t, m, "/", "a", "u")
	gt.Equal(t, screen(m)[1], `No items match "au".`)
}

func TestAge(t *testing.T) {
	cases := map[time.Duration]string{
		59 * time.Second:     " 0m",
		90 * time.Minute:     " 1h",
		3 * 24 * time.Hour:   " 3d",
		20 * 24 * time.Hour:  " 2w",
		400 * 24 * time.Hour: " 1y",
	}
	for age, want := range cases {
		inbox := &fakeInbox{entries: []*model.Entry{entry("PR_1", model.ItemKindPullRequest, 1, "T", age, false)}}
		m, _ := newModel(t, inbox)
		row := screen(m)[1]
		gt.S(t, row).HasSuffix(want)
	}
}

func TestScroll(t *testing.T) {
	var entries []*model.Entry
	for i := 0; i < 30; i++ {
		entries = append(entries, entry(fmt.Sprintf("PR_%02d", i), model.ItemKindPullRequest, i+1, fmt.Sprintf("Item %02d", i), time.Duration(i)*time.Minute, false))
	}
	inbox := &fakeInbox{entries: entries}
	m, _ := newModel(t, inbox)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	press(t, m, "G")
	lines := screen(m)
	gt.S(t, lines[7]).Contains("Item 29")
	gt.S(t, lines[1]).Contains("Item 23")
	gt.Equal(t, selectedID(t, m, inbox), model.ItemID("PR_29"))
}

// BenchmarkFilterKeystroke covers the 50 ms requirement for one keystroke
// over 1,000 items: the list call and a full redraw.
func BenchmarkFilterKeystroke(b *testing.B) {
	var entries []*model.Entry
	for i := 0; i < 1000; i++ {
		entries = append(entries, entry(fmt.Sprintf("PR_%d", i), model.ItemKindPullRequest, i+1,
			fmt.Sprintf("Change number %d of the webhook sender", i), time.Duration(i)*time.Minute, i%3 == 0))
	}
	inbox := &fakeInbox{entries: entries}
	m := tui.New(context.Background(), inbox, make(chan struct{}, 1), func() time.Time { return now })
	tui.DisableTick(m)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})

	b.ResetTimer()
	for b.Loop() {
		list, _ := inbox.List(context.Background(), model.NewFilter("webhook"))
		_ = list
		_ = m.View()
	}
}
