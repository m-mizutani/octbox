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

// RunWithOptions is Run with extra program options (input and output for
// tests).
func RunWithOptions(ctx context.Context, m *Model, events <-chan usecase.SyncEvent, opts ...tea.ProgramOption) error {
	return run(ctx, m, events, opts...)
}
