package safe_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/utils/logging"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

type closer struct{ err error }

func (c *closer) Close() error { return c.err }

func TestClose(t *testing.T) {
	var buf bytes.Buffer
	ctx := logging.With(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)))

	safe.Close(ctx, nil)
	safe.Close(ctx, &closer{})
	gt.Equal(t, buf.String(), "")

	safe.Close(ctx, &closer{err: errors.New("disk full")})
	gt.S(t, buf.String()).Contains("failed to close")
	gt.S(t, buf.String()).Contains("disk full")
}
