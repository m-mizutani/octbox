// Package inputsource implements interfaces.InputSource with the macOS Text
// Input Sources API (Carbon), called without cgo. carbon_darwin.go loads the
// API; unsupported.go makes New fail on every other OS.
package inputsource

import (
	"sync"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

// sourceRef is a TISInputSourceRef. Zero is NULL.
type sourceRef uintptr

// textInputSources is the part of the Text Input Sources API the Switcher
// uses. The copy functions return a reference the caller must release.
type textInputSources interface {
	// refreshCurrent lets the process see an input source selected by
	// another process since the last call.
	refreshCurrent()
	copyCurrent() sourceRef
	copyASCIICapable() sourceRef
	sameID(a, b sourceRef) bool
	// selectSource returns the OSStatus; 0 is success.
	selectSource(src sourceRef) int32
	release(src sourceRef)
}

// Switcher implements interfaces.InputSource.
type Switcher struct {
	mu       sync.Mutex
	api      textInputSources
	previous sourceRef // kept by SelectASCIICapable; zero when none
}

var _ interfaces.InputSource = (*Switcher)(nil)

func (x *Switcher) SelectASCIICapable() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.previous != 0 {
		return nil
	}

	x.api.refreshCurrent()
	current := x.api.copyCurrent()
	if current == 0 {
		return goerr.New("failed to read the current input source")
	}
	ascii := x.api.copyASCIICapable()
	if ascii == 0 {
		x.api.release(current)
		return goerr.New("failed to read the ASCII-capable keyboard layout")
	}
	defer x.api.release(ascii)

	if x.api.sameID(current, ascii) {
		x.api.release(current)
		return nil
	}
	if status := x.api.selectSource(ascii); status != 0 {
		x.api.release(current)
		return goerr.New("failed to select the ASCII-capable keyboard layout", goerr.V("status", status))
	}
	x.previous = current
	return nil
}

func (x *Switcher) RestorePrevious() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.previous == 0 {
		return nil
	}

	previous := x.previous
	x.previous = 0
	defer x.api.release(previous)
	if status := x.api.selectSource(previous); status != 0 {
		return goerr.New("failed to select the previous input source", goerr.V("status", status))
	}
	return nil
}
