package browser

import "context"

func NewWithRunner(goos string, run func(ctx context.Context, name string, args ...string) error) *Browser {
	return &Browser{goos: goos, run: run}
}
