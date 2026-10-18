package model

import "time"

type SyncError string

const (
	SyncErrorNone         SyncError = ""
	SyncErrorUnauthorized SyncError = "unauthorized"
	SyncErrorRateLimited  SyncError = "rate_limited"
	SyncErrorNetwork      SyncError = "network"
	SyncErrorAPI          SyncError = "api"
	SyncErrorStorage      SyncError = "storage"
	SyncErrorUnknown      SyncError = "unknown"
)

type RateLimit struct {
	Remaining int // -1 when unknown (REST response without headers)
	ResetAt   time.Time
}

// SyncState is the only row of the sync state. Every process that shares the
// database reads it; only the process holding the lease writes it.
type SyncState struct {
	Viewer                   string
	SearchCursor             time.Time // start time of the last completed search step
	NotificationCursor       time.Time
	NotificationLastModified string
	LastFullSyncAt           time.Time
	LastOpenRefreshAt        time.Time
	LastSyncAt               time.Time // end of the last attempt, success or failure
	NextRunAt                time.Time
	EarliestRunAt            time.Time // a forced sync may not start before this
	LeaseUntil               time.Time
	ConsecutiveFailures      int
	LastError                SyncError
	RateRemaining            int // -1 when unknown
	RateResetAt              time.Time
}

// NewSyncState returns the state of a database that has never synced.
func NewSyncState() *SyncState {
	return &SyncState{RateRemaining: -1}
}
