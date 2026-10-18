// Package browser opens URLs in the default browser of the OS.
package browser

import (
	"context"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

// Browser implements interfaces.Browser.
type Browser struct {
	goos string
	run  func(ctx context.Context, name string, args ...string) error
}

var _ interfaces.Browser = (*Browser)(nil)

func New() *Browser {
	return &Browser{goos: runtime.GOOS, run: runCommand}
}

func runCommand(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func (x *Browser) Open(ctx context.Context, rawURL string) error {
	// Only web URLs are passed to the OS: open(1) and xdg-open also accept
	// file paths and custom schemes.
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return goerr.New("refused to open a non-web URL", goerr.V("url", rawURL))
	}

	var command string
	switch x.goos {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return goerr.Wrap(interfaces.ErrUnsupportedOS, "cannot open a browser", goerr.V("goos", x.goos))
	}
	if err := x.run(ctx, command, rawURL); err != nil {
		return goerr.Wrap(err, "failed to open the browser", goerr.V("command", command), goerr.V("url", rawURL))
	}
	return nil
}
