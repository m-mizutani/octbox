package usecase

import "time"

// WithCheckInterval shortens the 5-second check of Run in tests.
func WithCheckInterval(d time.Duration) SyncerOption {
	return func(x *Syncer) { x.checkInterval = d }
}
