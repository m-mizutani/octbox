package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

var (
	colorGreen  = lipgloss.Color("2")
	colorRed    = lipgloss.Color("1")
	colorYellow = lipgloss.Color("3")
	colorGray   = lipgloss.Color("8")
	colorAccent = lipgloss.Color("6")
)

const (
	reviewWidth  = 8
	maxRepoWidth = 32
	authorWidth  = 16
	ageWidth     = 3
	minTitle     = 10
	minRepoWidth = 5
	// Both fit in 80 columns; j/k is left out to make room.
	helpTextInbox = "enter open  / filter  m read  e archive  a all  A read all  r sync  q quit"
	helpTextAll   = "enter open  / filter  m read  e archive  a inbox  A read all  r sync  q quit"
)

func (m *Model) helpText() string {
	if m.listedScope == model.FilterScopeAll {
		return helpTextAll
	}
	return helpTextInbox
}

func (m *Model) View() tea.View {
	var b strings.Builder
	b.WriteString(m.headerLine())
	b.WriteString("\n")

	h := m.listHeight()
	lines := m.listLines(h)
	for i := 0; i < h; i++ {
		if i < len(lines) {
			b.WriteString(lines[i])
		}
		b.WriteString("\n")
	}

	b.WriteString(m.statusLine())
	b.WriteString("\n")
	if m.filtering {
		b.WriteString(m.input.View())
	} else {
		b.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render(truncate(m.helpText(), m.width)))
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m *Model) headerLine() string {
	unread := 0
	for _, e := range m.entries {
		if e.Unread() {
			unread++
		}
	}
	accent := lipgloss.NewStyle().Foreground(colorAccent)
	return accent.Bold(true).Render("octbox") + "  " +
		accent.Render(string(m.listedScope)) + "  " +
		accent.Render(fmt.Sprintf("%d unread", unread)) +
		fmt.Sprintf(" / %d items", len(m.entries))
}

func (m *Model) listLines(h int) []string {
	if len(m.entries) == 0 {
		var text string
		switch {
		case !m.loaded:
			text = "Syncing with GitHub…"
		case m.keyword() != "":
			text = fmt.Sprintf("No items match %q.", m.keyword())
		case m.listedScope == model.FilterScopeInbox:
			text = "No items in the inbox. Press a to show all items."
		default:
			text = "No items."
		}
		return []string{lipgloss.NewStyle().Foreground(colorGray).Render(text)}
	}

	layout := m.layout()
	end := min(len(m.entries), m.offset+h)
	lines := make([]string, 0, end-m.offset)
	for i := m.offset; i < end; i++ {
		lines = append(lines, m.row(m.entries[i], layout, i == m.cursor))
	}
	return lines
}

type rowLayout struct {
	repo       int
	title      int
	showAuthor bool
	// showMark adds the column telling why the inbox does not list a row;
	// only the all scope lists such rows.
	showMark bool
}

func repoLabel(item *model.Item) string {
	return item.Repo.String() + "#" + strconv.Itoa(item.Number)
}

// layout fits a row into the screen width: the title column takes what the
// fixed columns leave, author and age are dropped below 60 columns, and the
// repository column shrinks when the title would get fewer than 10 columns.
func (m *Model) layout() rowLayout {
	repo := minRepoWidth
	for _, e := range m.entries {
		repo = max(repo, lipgloss.Width(repoLabel(&e.Item)))
	}
	repo = min(repo, maxRepoWidth)

	l := rowLayout{repo: repo, showAuthor: m.width >= minimumWideWidth, showMark: m.listedScope == model.FilterScopeAll}
	// unread, kind, CI, review and the spaces after each, then the space
	// after the repository column.
	fixed := 1 + 1 + 1 + 1 + 1 + 1 + reviewWidth + 1 + 1
	if l.showAuthor {
		fixed += 1 + authorWidth + 1 + ageWidth
	}
	if l.showMark {
		fixed += 1 + 1
	}
	l.title = m.width - fixed - l.repo
	if l.title < minTitle {
		l.repo = max(minRepoWidth, l.repo-(minTitle-l.title))
		l.title = max(minTitle, m.width-fixed-l.repo)
	}
	return l
}

func (m *Model) row(e *model.Entry, l rowLayout, selected bool) string {
	base := lipgloss.NewStyle()
	if selected {
		base = base.Reverse(true)
	}
	sp := base.Render(" ")
	item := &e.Item

	// Read rows draw their text in gray. Kind, CI and review keep their
	// colors so draft, CI and review states can still be told at a glance.
	text := base
	unread := base.Render(" ")
	if e.Unread() {
		unread = base.Foreground(colorAccent).Render("●")
	} else {
		text = base.Foreground(colorGray)
	}

	kindLetter := "I"
	if item.Kind == model.ItemKindPullRequest {
		kindLetter = "P"
	}
	kindColor := colorGreen
	if item.IsDraft {
		kindColor = colorGray
	}
	kind := base.Foreground(kindColor).Render(kindLetter)

	var ci string
	switch item.CI {
	case model.CIStateSuccess:
		ci = base.Foreground(colorGreen).Render("✓")
	case model.CIStateFailure:
		ci = base.Foreground(colorRed).Render("✗")
	case model.CIStatePending:
		ci = base.Foreground(colorYellow).Render("●")
	default:
		ci = base.Render(" ")
	}

	var review string
	switch item.Review {
	case model.ReviewStateApproved:
		review = base.Foreground(colorGreen).Render(pad("approved", reviewWidth))
	case model.ReviewStateChangesRequested:
		review = base.Foreground(colorRed).Render(pad("changes", reviewWidth))
	case model.ReviewStateRequired:
		review = base.Foreground(colorYellow).Render(pad("review", reviewWidth))
	default:
		review = base.Render(pad("", reviewWidth))
	}

	repo := text.Render(pad(truncate(repoLabel(item), l.repo), l.repo))
	titleStyle := text
	if e.Unread() {
		titleStyle = titleStyle.Bold(true)
	}
	title := titleStyle.Render(pad(truncate(item.Title, l.title), l.title))

	parts := []string{unread, kind, ci, review, repo, title}
	if l.showMark {
		// The archive is shown first when both apply: e can end it.
		mark := " "
		switch {
		case e.Archived():
			mark = "a"
		case item.ViewerReviewDone():
			mark = "d"
		}
		parts = append([]string{base.Foreground(colorGray).Render(mark)}, parts...)
	}
	if l.showAuthor {
		author := text.Render(pad(truncate("@"+item.Author, authorWidth), authorWidth))
		age := text.Render(padLeft(formatAge(m.now().Sub(item.UpdatedAt)), ageWidth))
		parts = append(parts, author, age)
	}
	return strings.Join(parts, sp)
}

func (m *Model) statusLine() string {
	if m.flash != "" {
		style := lipgloss.NewStyle()
		if m.flashStyle == flashError {
			style = style.Foreground(colorRed)
		}
		return style.Render(truncate(m.flash, m.width))
	}

	text, fg := m.syncStatus()
	filter := "(none)"
	if kw := m.keyword(); kw != "" {
		filter = kw
	}
	text += " · filter: " + filter
	style := lipgloss.NewStyle().Foreground(colorGray)
	if fg != nil {
		style = lipgloss.NewStyle().Foreground(fg)
	}
	return style.Render(truncate(text, m.width))
}

func clock(t time.Time) string {
	return t.Local().Format("15:04")
}

func (m *Model) syncStatus() (string, color.Color) {
	st := &m.syncState
	if m.syncing {
		return "Syncing…", nil
	}
	switch st.LastError {
	case model.SyncErrorUnauthorized:
		return "GitHub rejected the token. Run `octbox auth login`.", colorRed
	case model.SyncErrorRateLimited:
		return "Rate limited by GitHub. Next sync at " + clock(st.NextRunAt) + ".", colorYellow
	case model.SyncErrorNetwork, model.SyncErrorAPI, model.SyncErrorStorage, model.SyncErrorUnknown:
		return "Sync failed (" + syncErrorText(st.LastError) + "). Retrying at " + clock(st.NextRunAt) + ".", colorRed
	}
	if st.RateRemaining >= 0 && st.RateRemaining < lowRateThreshold {
		return "Rate limited by GitHub. Next sync at " + clock(st.NextRunAt) + ".", colorYellow
	}
	if !st.LastSyncAt.IsZero() {
		return "Synced " + clock(st.LastSyncAt) + " · next " + clock(st.NextRunAt), nil
	}
	return "Not synced yet", nil
}

// lowRateThreshold matches the syncer: below it, the syncer waits for the
// rate limit reset even after a successful sync.
const lowRateThreshold = 200

func syncErrorText(e model.SyncError) string {
	switch e {
	case model.SyncErrorNetwork:
		return "network error"
	case model.SyncErrorAPI:
		return "GitHub API error"
	case model.SyncErrorStorage:
		return "local database error"
	default:
		return "unexpected error"
	}
}

func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d < 14*24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d < 365*24*time.Hour:
		return strconv.Itoa(int(d/(7*24*time.Hour))) + "w"
	default:
		return strconv.Itoa(int(d/(365*24*time.Hour))) + "y"
	}
}

// truncate cuts s to at most width display columns, ending with "…" when
// it was cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteString("…")
	return b.String()
}

func pad(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func padLeft(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}
