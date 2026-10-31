package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/m-mizutani/octbox/pkg/usecase"
)

// DisableTick stops the one-second redraw so tests run every command
// synchronously.
func DisableTick(m *Model) {
	m.tick = 0
}

// ListCmd returns the command that reads the list for the current keyword
// and scope, so a test can deliver its response late.
func ListCmd(m *Model) tea.Cmd {
	return m.listCmd()
}

// RunWithOptions is Run with extra program options (input and output for
// tests).
func RunWithOptions(ctx context.Context, m *Model, events <-chan usecase.SyncEvent, opts ...tea.ProgramOption) error {
	return run(ctx, m, events, opts...)
}
