package config

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"
)

// minInterval keeps the GraphQL usage near the budget estimated for the
// 60-second default.
const minInterval = 30 * time.Second

type Sync struct {
	interval time.Duration
}

func (x *Sync) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.DurationFlag{
			Name:        "interval",
			Category:    "Sync",
			Usage:       "Interval between syncs with GitHub (at least 30s)",
			Value:       time.Minute,
			Sources:     cli.EnvVars("OCTBOX_INTERVAL"),
			Destination: &x.interval,
			Local:       true,
		},
	}
}

func (x *Sync) Interval() (time.Duration, error) {
	if x.interval < minInterval {
		return 0, goerr.New("--interval must be at least 30s", goerr.V("interval", x.interval.String()))
	}
	return x.interval, nil
}
