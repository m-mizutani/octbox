package usecase

import (
	"context"
	"time"
)

// WithCheckInterval shortens the 5-second check of Run in tests.
func WithCheckInterval(d time.Duration) SyncerOption {
	return func(x *Syncer) { x.checkInterval = d }
}

// WithSleep replaces the wait before a retried GitHub request in tests.
func WithSleep(sleep func(ctx context.Context, d time.Duration) error) SyncerOption {
	return func(x *Syncer) { x.sleep = sleep }
}
