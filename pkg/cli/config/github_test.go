package config_test

import (
	"context"
	"os"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/cli/config"
)

func parse(t *testing.T, flags []cli.Flag, args ...string) {
	t.Helper()
	cmd := &cli.Command{Name: "test", Flags: flags, Action: func(context.Context, *cli.Command) error { return nil }}
	gt.NoError(t, cmd.Run(context.Background(), append([]string{"test"}, args...))).Required()
}

func TestGitHubClientID(t *testing.T) {
	t.Setenv("OCTBOX_CLIENT_ID", "")
	gt.NoError(t, os.Unsetenv("OCTBOX_CLIENT_ID"))

	var gh config.GitHub
	parse(t, gh.Flags())
	gt.Equal(t, gh.ClientID(), config.DefaultClientID)
	gt.Equal(t, config.DefaultClientID, "Ov23lifmj6pSAE32qsGZ")
	_, err := gh.NewDeviceAuthenticator()
	gt.NoError(t, err)

	var other config.GitHub
	parse(t, other.Flags(), "--client-id", "Iv1.other")
	gt.Equal(t, other.ClientID(), "Iv1.other")

	var empty config.GitHub
	parse(t, empty.Flags(), "--client-id", "")
	_, err = empty.NewDeviceAuthenticator()
	gt.Error(t, err)
}
