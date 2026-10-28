package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/utils/async"
)

// Run starts the program and forwards each SyncEvent as SyncEventMsg. It
// returns when the user quits or ctx is done.
func Run(ctx context.Context, m *Model, events <-chan usecase.SyncEvent) error {
	return run(ctx, m, events)
}

func run(ctx context.Context, m *Model, events <-chan usecase.SyncEvent, opts ...tea.ProgramOption) error {
	ctx, cancel := context.WithCancel(ctx)
	p := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...)
	wait := async.Go(ctx, "forward sync events", func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case e := <-events:
				p.Send(SyncEventMsg(e))
			}
		}
	})
	// The forwarder stops on ctx, so ctx is cancelled before waiting for it.
	defer func() {
		cancel()
		wait()
	}()

	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return goerr.Wrap(err, "failed to run the inbox screen")
	}
	return nil
}
