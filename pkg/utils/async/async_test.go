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
