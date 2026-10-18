package tui_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/controller/tui"
	"github.com/m-mizutani/octbox/pkg/usecase"
)

func TestRunReturnsWhenTheUserQuits(t *testing.T) {
	inbox := &fakeInbox{entries: threeEntries()}
	m := tui.New(context.Background(), inbox, make(chan struct{}, 1), func() time.Time { return now })

	done := make(chan error, 1)
	go func() {
		// The parent context is never cancelled: only "q" ends the program.
		done <- tui.RunWithOptions(context.Background(), m, make(chan usecase.SyncEvent),
			tea.WithInput(strings.NewReader("q")), tea.WithOutput(io.Discard), tea.WithWindowSize(80, 24))
	}()

	select {
	case err := <-done:
		gt.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after q")
	}
}
