package config

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/adapter/inputsource"
	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

type Keyboard struct {
	switchASCIIInputSource bool
	// switchASCIIInputSourceGiven is true when the flag or its environment
	// variable is given, as opposed to the default value.
	switchASCIIInputSourceGiven bool
}

func (x *Keyboard) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:     "switch-ascii-input-source",
			Category: "Keyboard",
			Usage: "Select the ASCII-capable input source while the inbox has focus, so its keys work " +
				"while an IME is selected; the previous source is selected again for the filter input, " +
				"when the inbox loses focus, and on exit (macOS only; the default is ignored on other OSes)",
			Value:       true,
			Sources:     cli.EnvVars("OCTBOX_SWITCH_ASCII_INPUT_SOURCE"),
			Destination: &x.switchASCIIInputSource,
			Local:       true,
			Action: func(context.Context, *cli.Command, bool) error {
				x.switchASCIIInputSourceGiven = true
				return nil
			},
		},
	}
}

// NewInputSource returns the input source switcher, or nil when the flag is
// off. On an OS without input source switching, the default value gives nil
// so octbox runs there unchanged; only a value the user gave is an error.
func (x *Keyboard) NewInputSource() (interfaces.InputSource, error) {
	if !x.switchASCIIInputSource {
		return nil, nil
	}
	s, err := inputsource.New()
	if errors.Is(err, interfaces.ErrInputSourceUnsupported) && !x.switchASCIIInputSourceGiven {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}
