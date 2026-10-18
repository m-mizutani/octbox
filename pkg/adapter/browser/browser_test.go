package browser_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/browser"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

type recorder struct {
	name string
	args []string
	err  error
}

func (r *recorder) run(_ context.Context, name string, args ...string) error {
	r.name = name
	r.args = args
	return r.err
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	const u = "https://github.com/example-org/api/pull/1"

	for goos, command := range map[string]string{"darwin": "open", "linux": "xdg-open"} {
		t.Run(goos, func(t *testing.T) {
			r := &recorder{}
			gt.NoError(t, browser.NewWithRunner(goos, r.run).Open(ctx, u))
			gt.Equal(t, r.name, command)
			gt.A(t, r.args).Equal([]string{u})
		})
	}

	t.Run("unsupported OS", func(t *testing.T) {
		r := &recorder{}
		gt.Error(t, browser.NewWithRunner("windows", r.run).Open(ctx, u)).Is(interfaces.ErrUnsupportedOS)
		gt.Equal(t, r.name, "")
	})

	t.Run("command failure", func(t *testing.T) {
		r := &recorder{err: errors.New("exit status 3")}
		gt.Error(t, browser.NewWithRunner("linux", r.run).Open(ctx, u))
	})

	t.Run("non-web URL", func(t *testing.T) {
		r := &recorder{}
		gt.Error(t, browser.NewWithRunner("darwin", r.run).Open(ctx, "file:///etc/passwd"))
		gt.Equal(t, r.name, "")
	})
}
