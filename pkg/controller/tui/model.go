// Package tui is the full-screen inbox. It turns key presses into Inbox calls
// and draws the result; it holds only display state and makes no business
// decision.
package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/utils/errutil"
)

// Inbox is the part of usecase.Inbox the screen uses.
type Inbox interface {
	List(ctx context.Context, filter model.Filter) ([]*model.Entry, error)
	Open(ctx context.Context, id model.ItemID) error
	ToggleRead(ctx context.Context, id model.ItemID) error
	MarkRead(ctx context.Context, ids []model.ItemID) error
	SyncState(ctx context.Context) (*model.SyncState, error)
}

// SyncEventMsg carries a sync event from the syncer goroutine.
type SyncEventMsg usecase.SyncEvent

type entriesMsg struct {
	keyword string
	entries []*model.Entry
	err     error
}

type syncStateMsg struct {
	state *model.SyncState
	err   error
}

// actionDoneMsg reports the end of an Open or read mark action. failure is
// the status line text when the action failed; sticky keeps it until the
// next key press.
type actionDoneMsg struct {
	failure string
	sticky  bool
}

type tickMsg time.Time

const (
	tickInterval     = time.Second
	flashDuration    = 5 * time.Second
	defaultWidth     = 80
	defaultHeight    = 24
	chromeLines      = 3 // header, status line, help line or input
	minimumWideWidth = 60
)

type Model struct {
	ctx     context.Context
	inbox   Inbox
	trigger chan<- struct{}
	now     func() time.Time

	entries    []*model.Entry
	cursor     int
	offset     int          // first visible row
	selectedID model.ItemID // kept across reloads

	input     textinput.Model
	filtering bool // input line has focus

	syncing   bool
	syncState model.SyncState
	loaded    bool // at least one sync event or non-empty list received

	flash      string // transient status message
	flashStyle flashKind
	flashUntil time.Time // zero: until the next key press

	width, height int
	tick          time.Duration // 0 disables the periodic redraw (tests)
}

type flashKind int

const (
	flashInfo flashKind = iota
	flashError
)

func New(ctx context.Context, inbox Inbox, trigger chan<- struct{}, now func() time.Time) *Model {
	input := textinput.New()
	input.Prompt = "/ "
	return &Model{
		ctx:     ctx,
		inbox:   inbox,
		trigger: trigger,
		now:     now,
		input:   input,
		width:   defaultWidth,
		height:  defaultHeight,
		tick:    tickInterval,
		syncState: model.SyncState{
			RateRemaining: -1,
		},
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.listCmd(), m.syncStateCmd(), m.tickCmd())
}

func (m *Model) tickCmd() tea.Cmd {
	if m.tick <= 0 {
		return nil
	}
	return tea.Tick(m.tick, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) keyword() string {
	return m.input.Value()
}

func (m *Model) listCmd() tea.Cmd {
	ctx, inbox, keyword := m.ctx, m.inbox, m.keyword()
	return func() tea.Msg {
		entries, err := inbox.List(ctx, model.NewFilter(keyword))
		errutil.Handle(ctx, err, "failed to list items")
		return entriesMsg{keyword: keyword, entries: entries, err: err}
	}
}

func (m *Model) syncStateCmd() tea.Cmd {
	ctx, inbox := m.ctx, m.inbox
	return func() tea.Msg {
		state, err := inbox.SyncState(ctx)
		errutil.Handle(ctx, err, "failed to read the sync state")
		return syncStateMsg{state: state, err: err}
	}
}

func (m *Model) openCmd(entry *model.Entry) tea.Cmd {
	ctx, inbox, id, url := m.ctx, m.inbox, entry.Item.ID, entry.Item.URL
	return func() tea.Msg {
		err := inbox.Open(ctx, id)
		errutil.Handle(ctx, err, "failed to open an item")
		switch {
		case err == nil:
			return actionDoneMsg{}
		case goerr.HasTag(err, usecase.TagBrowserFailed):
			return actionDoneMsg{failure: "Failed to open the browser. URL: " + url, sticky: true}
		case goerr.HasTag(err, usecase.TagOpenedNotMarked):
			return actionDoneMsg{failure: "Opened in your browser, but failed to mark the item as read."}
		default:
			return actionDoneMsg{failure: "Failed to read the local database."}
		}
	}
}

func readActionDone(err error) actionDoneMsg {
	if err != nil {
		return actionDoneMsg{failure: "Failed to update the read state."}
	}
	return actionDoneMsg{}
}

func (m *Model) toggleCmd(id model.ItemID) tea.Cmd {
	ctx, inbox := m.ctx, m.inbox
	return func() tea.Msg {
		err := inbox.ToggleRead(ctx, id)
		errutil.Handle(ctx, err, "failed to toggle the read mark")
		return readActionDone(err)
	}
}

func (m *Model) markReadCmd(ids []model.ItemID) tea.Cmd {
	ctx, inbox := m.ctx, m.inbox
	return func() tea.Msg {
		err := inbox.MarkRead(ctx, ids)
		errutil.Handle(ctx, err, "failed to mark items read")
		return readActionDone(err)
	}
}

func (m *Model) setFlash(kind flashKind, text string, d time.Duration) {
	m.flash = text
	m.flashStyle = kind
	m.flashUntil = time.Time{}
	if d > 0 {
		m.flashUntil = m.now().Add(d)
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
		return m, nil

	case tea.KeyPressMsg:
		if m.flash != "" && m.flashUntil.IsZero() {
			m.flash = ""
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.filtering {
			return m.updateFiltering(msg)
		}
		return m.updateList(msg)

	case entriesMsg:
		if msg.keyword != m.keyword() {
			return m, nil // a response to an older keyword
		}
		if msg.err != nil {
			m.setFlash(flashError, "Failed to read the local database.", flashDuration)
			return m, nil
		}
		m.setEntries(msg.entries)
		return m, nil

	case syncStateMsg:
		if msg.err == nil && msg.state != nil {
			m.syncState = *msg.state
			if !m.syncState.LastSyncAt.IsZero() {
				m.loaded = true
			}
		}
		return m, nil

	case SyncEventMsg:
		m.syncing = msg.Running
		m.syncState = msg.State
		if msg.Running {
			return m, nil
		}
		m.loaded = true
		return m, m.listCmd()

	case actionDoneMsg:
		switch {
		case msg.failure != "" && msg.sticky:
			m.setFlash(flashError, msg.failure, 0)
		case msg.failure != "":
			m.setFlash(flashError, msg.failure, flashDuration)
		}
		return m, m.listCmd()

	case tickMsg:
		if m.flash != "" && !m.flashUntil.IsZero() && !m.now().Before(m.flashUntil) {
			m.flash = ""
		}
		return m, m.tickCmd()
	}

	if m.filtering {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateFiltering(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.input.Blur()
		return m, nil
	case "esc":
		m.filtering = false
		m.input.Blur()
		m.input.Reset()
		return m, m.listCmd()
	case "down", "ctrl+n":
		m.move(1)
		return m, nil
	case "up", "ctrl+p":
		m.move(-1)
		return m, nil
	}

	before := m.keyword()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.keyword() != before {
		return m, tea.Batch(cmd, m.listCmd())
	}
	return m, cmd
}

func (m *Model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down", "ctrl+n":
		m.move(1)
	case "k", "up", "ctrl+p":
		m.move(-1)
	case "g", "home":
		m.moveTo(0)
	case "G", "end":
		m.moveTo(len(m.entries) - 1)
	case "enter":
		if e := m.selected(); e != nil {
			return m, m.openCmd(e)
		}
	case "esc":
		if m.keyword() != "" {
			m.input.Reset()
			return m, m.listCmd()
		}
	case "/":
		m.filtering = true
		cmd := m.input.Focus()
		m.input.CursorEnd()
		return m, cmd
	case "m":
		if e := m.selected(); e != nil {
			return m, m.toggleCmd(e.Item.ID)
		}
	case "A":
		if len(m.entries) > 0 {
			ids := make([]model.ItemID, len(m.entries))
			for i, e := range m.entries {
				ids[i] = e.Item.ID
			}
			return m, m.markReadCmd(ids)
		}
	case "r":
		m.requestSync()
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// requestSync asks the syncer for a sync without blocking. A sync already
// requested and not yet taken is enough, so a full channel drops the request.
func (m *Model) requestSync() {
	if m.now().Before(m.syncState.EarliestRunAt) {
		m.setFlash(flashInfo, "Next sync is allowed at "+m.syncState.EarliestRunAt.Local().Format("15:04:05")+".", flashDuration)
		return
	}
	select {
	case m.trigger <- struct{}{}:
	default:
	}
}

func (m *Model) selected() *model.Entry {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return m.entries[m.cursor]
}

func (m *Model) move(delta int) {
	m.moveTo(m.cursor + delta)
}

func (m *Model) moveTo(i int) {
	if len(m.entries) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = max(0, min(i, len(m.entries)-1))
	m.selectedID = m.entries[m.cursor].Item.ID
	m.scroll()
}

// setEntries replaces the list and keeps the selected item selected when it
// is still listed; otherwise the same row number stays selected.
func (m *Model) setEntries(entries []*model.Entry) {
	m.entries = entries
	if len(entries) > 0 {
		m.loaded = true
	}
	for i, e := range entries {
		if e.Item.ID == m.selectedID {
			m.cursor = i
			m.scroll()
			return
		}
	}
	m.moveTo(m.cursor)
}

func (m *Model) listHeight() int {
	return max(1, m.height-chromeLines)
}

func (m *Model) scroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(m.entries)-h)))
}
