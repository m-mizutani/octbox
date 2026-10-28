package safe

import (
	"context"
	"io"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/utils/errutil"
)

// Close closes closer and records a failure through errutil.Handle. A nil
// closer is ignored.
func Close(ctx context.Context, closer io.Closer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to close"), "failed to close")
	}
}
