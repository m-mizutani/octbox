package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/cli/config"
)

func TestSyncInterval(t *testing.T) {
	t.Setenv("OCTBOX_INTERVAL", "")
	gt.NoError(t, os.Unsetenv("OCTBOX_INTERVAL"))

	var def config.Sync
	parse(t, def.Flags())
	d, err := def.Interval()
	gt.NoError(t, err).Required()
	gt.Equal(t, d, time.Minute)

	var short config.Sync
	parse(t, short.Flags(), "--interval", "20s")
	_, err = short.Interval()
	gt.Error(t, err).Contains("--interval must be at least 30s")

	t.Setenv("OCTBOX_INTERVAL", "90s")
	var env config.Sync
	parse(t, env.Flags())
	d, err = env.Interval()
	gt.NoError(t, err).Required()
	gt.Equal(t, d, 90*time.Second)
}
