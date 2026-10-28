package errutil_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/utils/errutil"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

func TestHandle(t *testing.T) {
	var buf bytes.Buffer
	ctx := logging.With(context.Background(), slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	errutil.Handle(ctx, nil, "nothing")
	gt.Equal(t, buf.String(), "")

	errutil.Handle(ctx, goerr.New("broken", goerr.V("item_id", "PR_1")), "failed")
	gt.S(t, buf.String()).Contains("level=ERROR")
	gt.S(t, buf.String()).Contains("item_id")
	buf.Reset()

	errutil.Handle(ctx, goerr.Wrap(errors.New("waiting"), "rate limited", goerr.T(errutil.TagBenign)), "benign")
	gt.S(t, buf.String()).Contains("level=INFO")
	buf.Reset()

	errutil.Handle(ctx, errors.New("plain"), "plain error")
	gt.S(t, buf.String()).Contains("plain")
}
