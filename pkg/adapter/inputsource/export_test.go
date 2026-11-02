package inputsource

// FakeSources is a Text Input Sources API with two sources, identified by
// their reference value. Every copy returns a new reference to one of them.
type FakeSources struct {
	Current      uintptr // the source the OS has selected
	ASCII        uintptr // the ASCII-capable keyboard layout
	SelectStatus int32   // returned by every select

	Refreshed int
	Selected  []uintptr
	// Retained counts the references copied and not yet released, per source.
	Retained map[uintptr]int
}

func NewWithFake(f *FakeSources) *Switcher {
	if f.Retained == nil {
		f.Retained = map[uintptr]int{}
	}
	return &Switcher{api: f}
}

func (f *FakeSources) refreshCurrent() { f.Refreshed++ }

func (f *FakeSources) copyCurrent() sourceRef {
	if f.Current != 0 {
		f.Retained[f.Current]++
	}
	return sourceRef(f.Current)
}

func (f *FakeSources) copyASCIICapable() sourceRef {
	if f.ASCII != 0 {
		f.Retained[f.ASCII]++
	}
	return sourceRef(f.ASCII)
}

func (f *FakeSources) sameID(a, b sourceRef) bool { return a == b }

func (f *FakeSources) selectSource(src sourceRef) int32 {
	f.Selected = append(f.Selected, uintptr(src))
	if f.SelectStatus == 0 {
		f.Current = uintptr(src)
	}
	return f.SelectStatus
}

func (f *FakeSources) release(src sourceRef) { f.Retained[uintptr(src)]-- }
