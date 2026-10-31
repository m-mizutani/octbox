package async_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/utils/async"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func capture() (context.Context, *syncBuffer) {
	buf := &syncBuffer{}
	return logging.With(context.Background(), slog.New(slog.NewTextHandler(buf, nil))), buf
}

func TestGoWaitsForTheHandler(t *testing.T) {
	ran := false
	wait := async.Go(context.Background(), "test", func(context.Context) error {
		ran = true
		return nil
	})
	wait()
	gt.True(t, ran)
}

func TestGoRecordsErrorsAndPanics(t *testing.T) {
	ctx, buf := capture()
	async.Go(ctx, "failing", func(context.Context) error { return errors.New("synthetic failure") })()
	gt.S(t, buf.String()).Contains("background goroutine failed")
	gt.S(t, buf.String()).Contains("synthetic failure")

	async.Go(ctx, "panicking", func(context.Context) error { panic("boom") })()
	gt.S(t, buf.String()).Contains("background goroutine panicked")
	gt.S(t, buf.String()).Contains("boom")
}

func TestMapReturnsResultsByIndexWithinTheLimit(t *testing.T) {
	var (
		mu              sync.Mutex
		running, peak   int
		release         = make(chan struct{})
		reachedTheLimit = make(chan struct{})
	)
	go func() {
		<-reachedTheLimit
		close(release)
	}()

	results, err := async.Map(context.Background(), 7, 3, func(_ context.Context, i int) (int, error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		if running == 3 && peak == 3 {
			select {
			case <-reachedTheLimit:
			default:
				close(reachedTheLimit)
			}
		}
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return i * 10, nil
	})
	gt.NoError(t, err)
	gt.A(t, results).Equal([]int{0, 10, 20, 30, 40, 50, 60})
	gt.Equal(t, peak, 3)
}

func TestMapStopsAtTheFirstError(t *testing.T) {
	results, err := async.Map(context.Background(), 5, 1, func(ctx context.Context, i int) (string, error) {
		if i == 2 {
			return "", errors.New("synthetic failure")
		}
		return "ok", nil
	})
	gt.Error(t, err)
	gt.S(t, err.Error()).Contains("synthetic failure")
	// With one call at a time, the calls after the failed one never start.
	gt.A(t, results).Equal([]string{"ok", "ok", "", "", ""})
}

func TestMapCancelsTheRunningCallsAtTheFirstError(t *testing.T) {
	var started sync.WaitGroup
	started.Add(2)
	cancelled := make([]bool, 3)
	_, err := async.Map(context.Background(), 3, 3, func(ctx context.Context, i int) (int, error) {
		if i == 2 {
			started.Wait()
			return 0, errors.New("synthetic failure")
		}
		started.Done()
		// Without the cancellation, these calls never return and Map hangs.
		<-ctx.Done()
		cancelled[i] = true
		return 0, ctx.Err()
	})
	gt.Error(t, err)
	gt.S(t, err.Error()).Contains("synthetic failure")
	gt.A(t, cancelled).Equal([]bool{true, true, false})
}

func TestMapConvertsAPanicIntoAnError(t *testing.T) {
	_, err := async.Map(context.Background(), 2, 2, func(_ context.Context, i int) (int, error) {
		if i == 1 {
			panic("boom")
		}
		return i, nil
	})
	gt.Error(t, err)
	gt.S(t, err.Error()).Contains("goroutine panicked")
}

func TestMapReturnsTheCancellationOfItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := async.Map(ctx, 3, 1, func(context.Context, int) (int, error) {
		calls++
		return 0, nil
	})
	gt.True(t, errors.Is(err, context.Canceled))
	gt.Equal(t, calls, 0)
}

func TestGoPassesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	wait := async.Go(ctx, "cancelled", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	<-started
	cancel()
	wait()
}
