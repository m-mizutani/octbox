package cli_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/cli"
)

func TestHerdrIndicator(t *testing.T) {
	full := map[string]string{
		"HERDR_ENV":         "1",
		"HERDR_PANE_ID":     "w1:p2",
		"HERDR_SOCKET_PATH": "/home/alice/.config/herdr/herdr.sock",
	}
	without := func(key, value string) map[string]string {
		env := map[string]string{}
		for k, v := range full {
			env[k] = v
		}
		if value == "" {
			delete(env, key)
		} else {
			env[key] = value
		}
		return env
	}

	cases := map[string]struct {
		env     map[string]string
		enabled bool
	}{
		"all set":                 {env: full, enabled: true},
		"HERDR_ENV unset":         {env: without("HERDR_ENV", "")},
		"HERDR_ENV is not 1":      {env: without("HERDR_ENV", "0")},
		"HERDR_PANE_ID empty":     {env: map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "", "HERDR_SOCKET_PATH": full["HERDR_SOCKET_PATH"]}},
		"HERDR_SOCKET_PATH unset": {env: without("HERDR_SOCKET_PATH", "")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				v, ok := c.env[key]
				return v, ok
			}
			gt.Equal(t, cli.HerdrIndicator(lookup), c.enabled)
		})
	}
}
