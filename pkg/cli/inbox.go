package cli

import (
	"context"
	"time"

	"github.com/m-mizutani/octbox/pkg/adapter/herdr"
	"github.com/m-mizutani/octbox/pkg/cli/config"
	"github.com/m-mizutani/octbox/pkg/controller/tui"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/usecase"
	"github.com/m-mizutani/octbox/pkg/utils/async"
	"github.com/m-mizutani/octbox/pkg/utils/errutil"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

// runInbox gets a token (logging in first when none is stored) and opens the
// inbox.
func runInbox(ctx context.Context, d *deps, storage *config.Storage, gh *config.GitHub, syncCfg *config.Sync) error {
	interval, err := syncCfg.Interval()
	if err != nil {
		return err
	}

	repo, err := storage.Open(ctx)
	if err != nil {
		return err
	}
	defer safe.Close(ctx, repo)

	auth, err := newAuth(d, gh, repo)
	if err != nil {
		return err
	}
	token, err := auth.EnsureToken(ctx, devicePrompt(ctx, d))
	if err != nil {
		return err
	}
	return d.startInbox(ctx, d, token, repo, interval)
}

// herdrIndicator returns the herdr pane octbox runs in when herdr provides
// HERDR_ENV=1, HERDR_PANE_ID, and HERDR_SOCKET_PATH, and nil otherwise.
func herdrIndicator(lookup func(string) (string, bool)) interfaces.UpdateIndicator {
	env, _ := lookup("HERDR_ENV")
	paneID, _ := lookup("HERDR_PANE_ID")
	socketPath, _ := lookup("HERDR_SOCKET_PATH")
	if env != "1" || paneID == "" || socketPath == "" {
		// An untyped nil: a nil *herdr.Client would be a non-nil interface.
		return nil
	}
	return herdr.New(socketPath, paneID)
}

func startInbox(ctx context.Context, d *deps, token model.GitHubToken, repo interfaces.Repository, interval time.Duration) error {
	syncer := usecase.NewSyncer(d.newClient(token), repo, interval)
	inbox := usecase.NewInbox(repo, d.newBrowser())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var detector *usecase.UpdateDetector
	if indicator := d.updateIndicator(); indicator != nil {
		detector = usecase.NewUpdateDetector(repo, indicator)
		errutil.Handle(ctx, detector.Start(ctx), "failed to start showing updates in herdr")
	}

	trigger := make(chan struct{}, 1)
	events := make(chan usecase.SyncEvent, 16)
	wait := async.Go(ctx, "syncer", func(ctx context.Context) error {
		return syncer.Run(ctx, trigger, func(e usecase.SyncEvent) {
			// When the screen is behind, the event is dropped: the next
			// 5-second check delivers the latest state.
			select {
			case events <- e:
			default:
			}
			// Checked after the screen is notified, so a slow herdr does not
			// delay the list.
			if detector != nil && !e.Running {
				errutil.Handle(ctx, detector.Check(ctx), "failed to show new updates in herdr")
			}
		})
	})

	err := tui.Run(ctx, tui.New(ctx, inbox, trigger, time.Now), events)
	cancel()
	wait()
	if detector != nil {
		// ctx is cancelled here; unregistering must still reach herdr.
		stopCtx := context.WithoutCancel(ctx)
		errutil.Handle(stopCtx, detector.Stop(stopCtx), "failed to unregister from herdr")
	}
	return err
}
