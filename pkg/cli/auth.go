package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/cli/config"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/utils/errutil"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

func cmdAuth(d *deps, storage *config.Storage, gh *config.GitHub) *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Log in to GitHub or log out",
		Commands: []*cli.Command{
			{
				Name:  "login",
				Usage: "Log in to GitHub with the device flow and store the token",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return runLogin(ctx, d, storage, gh)
				},
			},
			{
				Name:  "logout",
				Usage: "Delete the stored token (the local database is kept)",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return runLogout(ctx, d)
				},
			},
		},
	}
}

func newAuth(d *deps, gh *config.GitHub, repo interfaces.Repository) (*usecase.Auth, error) {
	device, err := d.newDevice(gh)
	if err != nil {
		return nil, err
	}
	return usecase.NewAuth(device, d.secureStore(), d.newClient, repo), nil
}

// devicePrompt prints the code and tries to open the verification page; the
// login continues when the browser does not open.
func devicePrompt(ctx context.Context, d *deps) func(code *model.DeviceCode) {
	return func(code *model.DeviceCode) {
		printDeviceCode(d.stdout, code)
		if err := d.newBrowser().Open(ctx, code.VerificationURI); err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "could not open the verification page", goerr.T(errutil.TagBenign)),
				"could not open the verification page")
		}
	}
}

func printDeviceCode(w io.Writer, code *model.DeviceCode) {
	_, _ = fmt.Fprintf(w, "First, copy your one-time code: %s\n", code.UserCode)
	_, _ = fmt.Fprintf(w, "Then open %s in your browser and enter the code.\n", code.VerificationURI)
	_, _ = fmt.Fprintln(w, "Waiting for authorization...")
}

func runLogin(ctx context.Context, d *deps, storage *config.Storage, gh *config.GitHub) error {
	repo, err := storage.Open(ctx)
	if err != nil {
		return err
	}
	defer safe.Close(ctx, repo)

	auth, err := newAuth(d, gh, repo)
	if err != nil {
		return err
	}
	viewer, err := auth.Login(ctx, devicePrompt(ctx, d))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(d.stdout, "Logged in as %s\n", viewer.Login)
	return nil
}

func runLogout(ctx context.Context, d *deps) error {
	auth := usecase.NewAuth(nil, d.secureStore(), d.newClient, nil)
	if err := auth.Logout(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(d.stdout, "Logged out. The local database is kept.")
	return nil
}
