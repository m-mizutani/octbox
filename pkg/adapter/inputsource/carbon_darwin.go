//go:build darwin

package inputsource

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/m-mizutani/goerr/v2"
)

const (
	carbonPath         = "/System/Library/Frameworks/Carbon.framework/Carbon"
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	libSystemPath      = "/usr/lib/libSystem.B.dylib"
)

// The current input source is cached per process and refreshed by a
// notification delivered only through the run loop of the main thread, so
// refreshCurrent must run there. Locking the main goroutine to the main
// thread in init keeps it there; the inbox screen calls the Switcher from the
// main goroutine (bubbletea runs Update on the goroutine that calls Run).
func init() {
	runtime.LockOSThread()
}

// carbon calls the Text Input Sources API through purego.
type carbon struct {
	copyCurrentKeyboardInputSource                   func() uintptr
	copyCurrentASCIICapableKeyboardLayoutInputSource func() uintptr
	getInputSourceProperty                           func(src, key uintptr) uintptr
	selectInputSource                                func(src uintptr) int32
	cfEqual                                          func(a, b uintptr) bool
	cfRelease                                        func(ref uintptr)
	cfRunLoopRunInMode                               func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	pthreadMainNP                                    func() int32

	propertyInputSourceID uintptr // kTISPropertyInputSourceID
	runLoopDefaultMode    uintptr // kCFRunLoopDefaultMode
}

func New() (*Switcher, error) {
	api, err := loadCarbon()
	if err != nil {
		return nil, err
	}
	return &Switcher{api: api}, nil
}

func loadCarbon() (*carbon, error) {
	c := &carbon{}
	libs := []struct {
		path    string
		funcs   map[string]any
		globals map[string]*uintptr
	}{
		{
			path: carbonPath,
			funcs: map[string]any{
				"TISCopyCurrentKeyboardInputSource":                   &c.copyCurrentKeyboardInputSource,
				"TISCopyCurrentASCIICapableKeyboardLayoutInputSource": &c.copyCurrentASCIICapableKeyboardLayoutInputSource,
				"TISGetInputSourceProperty":                           &c.getInputSourceProperty,
				"TISSelectInputSource":                                &c.selectInputSource,
			},
			globals: map[string]*uintptr{"kTISPropertyInputSourceID": &c.propertyInputSourceID},
		},
		{
			path: coreFoundationPath,
			funcs: map[string]any{
				"CFEqual":            &c.cfEqual,
				"CFRelease":          &c.cfRelease,
				"CFRunLoopRunInMode": &c.cfRunLoopRunInMode,
			},
			globals: map[string]*uintptr{"kCFRunLoopDefaultMode": &c.runLoopDefaultMode},
		},
		{
			path:  libSystemPath,
			funcs: map[string]any{"pthread_main_np": &c.pthreadMainNP},
		},
	}

	for _, lib := range libs {
		handle, err := purego.Dlopen(lib.path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to load a macOS framework", goerr.V("path", lib.path))
		}
		for name, fn := range lib.funcs {
			if _, err := purego.Dlsym(handle, name); err != nil {
				return nil, goerr.Wrap(err, "failed to find a macOS function", goerr.V("path", lib.path), goerr.V("name", name))
			}
			purego.RegisterLibFunc(fn, handle, name)
		}
		for name, dst := range lib.globals {
			addr, err := purego.Dlsym(handle, name)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to find a macOS constant", goerr.V("path", lib.path), goerr.V("name", name))
			}
			// addr is the address of a CFStringRef variable; reading it through
			// a pointer to addr keeps go vet's unsafeptr check satisfied.
			*dst = **(**uintptr)(unsafe.Pointer(&addr))
		}
	}
	return c, nil
}

func (c *carbon) refreshCurrent() {
	if c.pthreadMainNP() == 0 {
		return // not on the main thread; the cached source may be stale
	}
	// A zero-second run handles the notifications already delivered and
	// returns without waiting.
	c.cfRunLoopRunInMode(c.runLoopDefaultMode, 0, false)
}

func (c *carbon) copyCurrent() sourceRef {
	return sourceRef(c.copyCurrentKeyboardInputSource())
}

func (c *carbon) copyASCIICapable() sourceRef {
	return sourceRef(c.copyCurrentASCIICapableKeyboardLayoutInputSource())
}

func (c *carbon) sameID(a, b sourceRef) bool {
	left := c.getInputSourceProperty(uintptr(a), c.propertyInputSourceID)
	right := c.getInputSourceProperty(uintptr(b), c.propertyInputSourceID)
	return left != 0 && right != 0 && c.cfEqual(left, right)
}

func (c *carbon) selectSource(src sourceRef) int32 {
	return c.selectInputSource(uintptr(src))
}

func (c *carbon) release(src sourceRef) {
	c.cfRelease(uintptr(src))
}
