package inputsource_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/inputsource"
)

const (
	japanese uintptr = 1
	abc      uintptr = 2
)

func TestSelectASCIICapableThenRestore(t *testing.T) {
	f := &inputsource.FakeSources{Current: japanese, ASCII: abc}
	s := inputsource.NewWithFake(f)

	gt.NoError(t, s.SelectASCIICapable())
	gt.Equal(t, f.Current, abc)
	gt.Equal(t, f.Refreshed, 1)

	// A second call while the previous source is kept changes nothing.
	gt.NoError(t, s.SelectASCIICapable())
	gt.Equal(t, f.Selected, []uintptr{abc})

	gt.NoError(t, s.RestorePrevious())
	gt.Equal(t, f.Current, japanese)
	gt.Equal(t, f.Selected, []uintptr{abc, japanese})

	// Nothing is kept any more.
	gt.NoError(t, s.RestorePrevious())
	gt.Equal(t, f.Selected, []uintptr{abc, japanese})
	gt.Equal(t, f.Retained, map[uintptr]int{japanese: 0, abc: 0})
}

func TestSelectASCIICapableWhenAlreadySelected(t *testing.T) {
	f := &inputsource.FakeSources{Current: abc, ASCII: abc}
	s := inputsource.NewWithFake(f)

	gt.NoError(t, s.SelectASCIICapable())
	gt.NoError(t, s.RestorePrevious())
	gt.Equal(t, len(f.Selected), 0)
	gt.Equal(t, f.Retained, map[uintptr]int{abc: 0})
}

func TestSelectASCIICapableFails(t *testing.T) {
	f := &inputsource.FakeSources{Current: japanese, ASCII: abc, SelectStatus: -50}
	s := inputsource.NewWithFake(f)

	gt.Error(t, s.SelectASCIICapable()).Contains("failed to select the ASCII-capable keyboard layout")
	gt.Equal(t, f.Current, japanese)

	// The failed switch kept nothing to restore.
	gt.NoError(t, s.RestorePrevious())
	gt.Equal(t, f.Selected, []uintptr{abc})
	gt.Equal(t, f.Retained, map[uintptr]int{japanese: 0, abc: 0})
}

func TestSelectASCIICapableWithoutSource(t *testing.T) {
	f := &inputsource.FakeSources{Current: japanese}
	s := inputsource.NewWithFake(f)

	gt.Error(t, s.SelectASCIICapable()).Contains("failed to read the ASCII-capable keyboard layout")
	gt.Equal(t, len(f.Selected), 0)
	gt.Equal(t, f.Retained, map[uintptr]int{japanese: 0})
}

func TestRestorePreviousFails(t *testing.T) {
	f := &inputsource.FakeSources{Current: japanese, ASCII: abc}
	s := inputsource.NewWithFake(f)
	gt.NoError(t, s.SelectASCIICapable())

	f.SelectStatus = -50
	gt.Error(t, s.RestorePrevious()).Contains("failed to select the previous input source")
	// The kept source is released and forgotten, so a later call does not
	// retry with a released reference.
	gt.NoError(t, s.RestorePrevious())
	gt.Equal(t, f.Selected, []uintptr{abc, japanese})
	gt.Equal(t, f.Retained, map[uintptr]int{japanese: 0, abc: 0})
}
