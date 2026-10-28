// Package async starts background goroutines with panic recovery and error
// recording.
package async

import (
	"context"
	"fmt"
	"sync"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/utils/errutil"
)

// Go runs handler in a new goroutine with ctx, including its cancellation,
// unlike robin's Dispatch, which detaches the handler from the request that
// started it: octbox stops its background work when the inbox closes. An
// error or panic of the handler is recorded through errutil.Handle. The
// returned function blocks until the handler has returned.
func Go(ctx context.Context, name string, handler func(ctx context.Context) error) (wait func()) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				errutil.Handle(ctx, goerr.New("background goroutine panicked",
					goerr.V("name", name), goerr.V("panic", fmt.Sprint(r))), "background goroutine panicked")
			}
		}()
		if err := handler(ctx); err != nil {
			errutil.Handle(ctx, goerr.Wrap(err, "background goroutine failed", goerr.V("name", name)), "background goroutine failed")
		}
	}()
	return wg.Wait
}
