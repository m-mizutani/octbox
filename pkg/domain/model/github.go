package model

import "time"

// GitHubToken is an OAuth access token. Logging redacts every value of this
// type.
type GitHubToken string

// SecretKey names an entry of the OS secure storage.
type SecretKey string

const SecretKeyGitHubToken SecretKey = "github-token"

type DeviceCode struct {
	DeviceCode      string `masq:"secret"`
	UserCode        string // "ABCD-1234"
	VerificationURI string // "https://github.com/login/device"
	ExpiresAt       time.Time
	Interval        time.Duration // minimum polling interval
}

type Viewer struct {
	Login string
}

type SearchPage struct {
	Items       []Item
	EndCursor   string
	HasNextPage bool
	RateLimit   RateLimit
}

type ItemsResult struct {
	Items      []Item
	MissingIDs []ItemID // GetItems only: nodes returned as null
	RateLimit  RateLimit
}

type NotificationThread struct {
	Ref       ItemRef
	UpdatedAt time.Time
}

type NotificationPage struct {
	Threads      []NotificationThread // Issue and PullRequest subjects only
	NotModified  bool
	LastModified string        // value of the Last-Modified header; "" on 304
	PollInterval time.Duration // X-Poll-Interval; 0 when absent
}
