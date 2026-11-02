package config_test

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/cli/config"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

func TestKeyboardInputSourceIsOnByDefault(t *testing.T) {
	t.Setenv("OCTBOX_SWITCH_ASCII_INPUT_SOURCE", "")
	gt.NoError(t, os.Unsetenv("OCTBOX_SWITCH_ASCII_INPUT_SOURCE"))

	var k config.Keyboard
	parse(t, k.Flags())
	src, err := k.NewInputSource()
	gt.NoError(t, err)
	// The default is ignored where switching is not supported.
	gt.Equal(t, src != nil, runtime.GOOS == "darwin")
}

func TestKeyboardInputSourceCanBeTurnedOff(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		t.Setenv("OCTBOX_SWITCH_ASCII_INPUT_SOURCE", "")
		gt.NoError(t, os.Unsetenv("OCTBOX_SWITCH_ASCII_INPUT_SOURCE"))

		var k config.Keyboard
		parse(t, k.Flags(), "--switch-ascii-input-source=false")
		src, err := k.NewInputSource()
		gt.NoError(t, err)
		gt.True(t, src == nil)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("OCTBOX_SWITCH_ASCII_INPUT_SOURCE", "false")

		var k config.Keyboard
		parse(t, k.Flags())
		src, err := k.NewInputSource()
		gt.NoError(t, err)
		gt.True(t, src == nil)
	})
}

func TestKeyboardInputSourceGivenExplicitly(t *testing.T) {
	t.Setenv("OCTBOX_SWITCH_ASCII_INPUT_SOURCE", "true")

	var k config.Keyboard
	parse(t, k.Flags())
	src, err := k.NewInputSource()
	if runtime.GOOS != "darwin" {
		gt.True(t, errors.Is(err, interfaces.ErrInputSourceUnsupported))
		return
	}
	gt.NoError(t, err)
	gt.True(t, src != nil)
}
