//go:build darwin

package inputsource_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/inputsource"
)

// TestNewFindsTheAPI only loads the API: selecting a source would change the
// input source of the machine running the test.
func TestNewFindsTheAPI(t *testing.T) {
	s, err := inputsource.New()
	gt.NoError(t, err).Required()
	gt.NotNil(t, s)
}
