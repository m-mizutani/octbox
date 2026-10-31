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

// Map calls fn for every index in [0, n), running at most limit calls at
// once, and returns the results by index. Unlike Go, it returns the first
// error, or a panic converted into one, to the caller: after it no more calls
// start and the context of the running calls is cancelled. The results of
// the calls that succeeded are returned with the error; the others are the
// zero value.
func Map[T any](ctx context.Context, n, limit int, fn func(ctx context.Context, i int) (T, error)) ([]T, error) {
	results := make([]T, n)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
		slots    = make(chan struct{}, max(limit, 1))
	)
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}

	started := 0
	for i := 0; i < n; i++ {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		started++
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer func() {
				if r := recover(); r != nil {
					fail(goerr.New("goroutine panicked", goerr.V("index", i), goerr.V("panic", fmt.Sprint(r))))
				}
			}()
			v, err := fn(ctx, i)
			if err != nil {
				fail(err)
				return
			}
			results[i] = v
		}()
	}
	wg.Wait()

	if firstErr == nil && started < n {
		// The parent context was cancelled before every call started.
		firstErr = ctx.Err()
	}
	return results, firstErr
}
