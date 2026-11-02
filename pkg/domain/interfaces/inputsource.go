package interfaces

// InputSource switches the keyboard input source of the OS, so that the keys
// of the inbox reach it as ASCII while an input method (IME) is selected.
type InputSource interface {
	// SelectASCIICapable selects the ASCII-capable keyboard layout and keeps
	// the input source it replaced. While one is kept, it does nothing.
	SelectASCIICapable() error
	// RestorePrevious selects the input source kept by SelectASCIICapable
	// and forgets it. When none is kept, it does nothing.
	RestorePrevious() error
}
