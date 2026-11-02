//go:build !darwin

package inputsource

import (
	"runtime"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

func New() (*Switcher, error) {
	return nil, goerr.Wrap(interfaces.ErrInputSourceUnsupported, "cannot switch the input source", goerr.V("goos", runtime.GOOS))
}
