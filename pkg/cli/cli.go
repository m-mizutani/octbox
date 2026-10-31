// Package cli parses flags and environment variables, builds the components,
// and wires them for the octbox commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/adapter/browser"
	"github.com/m-mizutani/octbox/pkg/adapter/github"
	"github.com/m-mizutani/octbox/pkg/adapter/securestore"
	"github.com/m-mizutani/octbox/pkg/cli/config"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/utils/errutil"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

// deps are the external boundaries the commands use. Tests replace them.
type deps struct {
	stdout      io.Writer
	stderr      io.Writer
	secureStore func() interfaces.SecureStore
	newDevice   func(cfg *config.GitHub) (interfaces.DeviceAuthenticator, error)
	newClient   interfaces.GitHubClientFactory
	newBrowser  func() interfaces.Browser
	// herdrPane returns the herdr pane octbox runs in, or nil outside herdr.
	herdrPane func() herdrPane
	// startInbox runs the full-screen inbox with a token that GitHub accepted
	// or that is stored.
	startInbox func(ctx context.Context, d *deps, token model.GitHubToken, repo interfaces.Repository, interval time.Duration) error
}

func defaultDeps() *deps {
	return &deps{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		secureStore: securestore.New,
		newDevice: func(cfg *config.GitHub) (interfaces.DeviceAuthenticator, error) {
			return cfg.NewDeviceAuthenticator()
		},
		newClient:  func(token model.GitHubToken) interfaces.GitHubClient { return github.New(token) },
		newBrowser: func() interfaces.Browser { return browser.New() },
		herdrPane: func() herdrPane {
			return lookupHerdrPane(os.LookupEnv)
		},
		startInbox: startInbox,
	}
}

// Run executes the command line. An error that stops the command is recorded
// here and shown on stderr, so main only has to exit with a non-zero status.
func Run(ctx context.Context, args []string, version string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, args, version, defaultDeps())
}

func run(ctx context.Context, args []string, version string, d *deps) error {
	var (
		logger    config.Logger
		storage   config.Storage
		gh        config.GitHub
		syncCfg   config.Sync
		logCloser io.Closer
	)

	flags := append(logger.Flags(), storage.Flags()...)
	flags = append(flags, gh.Flags()...)
	flags = append(flags, syncCfg.Flags()...)

	app := &cli.Command{
		Name:      "octbox",
		Usage:     "List the GitHub issues and pull requests that involve you",
		Version:   version,
		Flags:     flags,
		Writer:    d.stdout,
		ErrWriter: d.stderr,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			closer, err := logger.Configure()
			if err != nil {
				return ctx, err
			}
			logCloser = closer
			return ctx, nil
		},
		Action: func(ctx context.Context, _ *cli.Command) error {
			return runInbox(ctx, d, &storage, &gh, &syncCfg)
		},
		Commands: []*cli.Command{
			cmdAuth(d, &storage, &gh),
		},
	}

	err := app.Run(ctx, args)
	if err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to run app"), "failed to run app")
		_, _ = fmt.Fprintln(d.stderr, "octbox: "+userMessage(err))
	}
	safe.Close(ctx, logCloser)
	return err
}

// userMessage turns an error into the line shown on the terminal: what
// happened and, when there is one, what to do next.
func userMessage(err error) string {
	switch {
	case errors.Is(err, interfaces.ErrDeviceCodeExpired):
		return "the code expired; run `octbox auth login` again"
	case errors.Is(err, interfaces.ErrAccessDenied):
		return "authorization was denied"
	case errors.Is(err, interfaces.ErrDeviceFlowDisabled):
		return "device flow is disabled for the OAuth app; enable it in the app settings"
	case errors.Is(err, interfaces.ErrSecureStoreUnavailable):
		return "secure storage is not available on this OS"
	case goerr.HasTag(err, usecase.TagTokenNotStored):
		return "failed to store the token in the OS secure storage"
	case errors.Is(err, interfaces.ErrUnauthorized):
		return "GitHub rejected the token; run `octbox auth login` again"
	case errors.Is(err, interfaces.ErrSchemaTooNew):
		return "the database was created by a newer version of octbox"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return err.Error()
}
