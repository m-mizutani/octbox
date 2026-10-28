package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

func TestRedaction(t *testing.T) {
	for name, format := range map[string]logging.Format{"console": logging.FormatConsole, "json": logging.FormatJSON} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.New(&buf, slog.LevelInfo, format, false)
			logger.Info("check",
				"token", model.GitHubToken("gho_secret_value"),
				"code", model.DeviceCode{DeviceCode: "device_secret_value", UserCode: "ABCD-1234"},
			)
			gt.S(t, buf.String()).NotContains("gho_secret_value")
			gt.S(t, buf.String()).NotContains("device_secret_value")
			gt.S(t, buf.String()).Contains("ABCD-1234")
		})
	}
}

func TestFrom(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := logging.With(context.Background(), logger)
	gt.Equal(t, logging.From(ctx), logger)
	gt.Equal(t, logging.From(context.Background()), logging.Default())
}
