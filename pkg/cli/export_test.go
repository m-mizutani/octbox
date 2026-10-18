package cli

import (
	"context"
	"io"
	"time"

	"github.com/m-mizutani/octbox/pkg/cli/config"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
)

// TestDeps replaces the external boundaries of the commands.
type TestDeps struct {
	Stdout      io.Writer
	Stderr      io.Writer
	SecureStore interfaces.SecureStore
	Device      interfaces.DeviceAuthenticator
	Client      interfaces.GitHubClient
	Browser     interfaces.Browser
	StartInbox  func(ctx context.Context, token model.GitHubToken, repo interfaces.Repository, interval time.Duration) error
}

func RunForTest(ctx context.Context, args []string, td TestDeps) error {
	d := &deps{
		stdout:      td.Stdout,
		stderr:      td.Stderr,
		secureStore: func() interfaces.SecureStore { return td.SecureStore },
		newDevice: func(*config.GitHub) (interfaces.DeviceAuthenticator, error) {
			return td.Device, nil
		},
		newClient:  func(model.GitHubToken) interfaces.GitHubClient { return td.Client },
		newBrowser: func() interfaces.Browser { return td.Browser },
		startInbox: func(ctx context.Context, _ *deps, token model.GitHubToken, repo interfaces.Repository, interval time.Duration) error {
			return td.StartInbox(ctx, token, repo, interval)
		},
	}
	return run(ctx, args, "test", d)
}
