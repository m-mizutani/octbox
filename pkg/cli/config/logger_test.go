package config_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/cli/config"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

func TestLoggerWritesOnlyToTheFile(t *testing.T) {
	t.Setenv("OCTBOX_LOG_LEVEL", "")
	gt.NoError(t, os.Unsetenv("OCTBOX_LOG_LEVEL"))
	path := filepath.Join(t.TempDir(), "logs", "octbox.log")

	r, w, err := os.Pipe()
	gt.NoError(t, err).Required()
	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	var logger config.Logger
	parse(t, logger.Flags(), "--log-file", path, "--log-level", "debug")
	closer, err := logger.Configure()
	gt.NoError(t, err).Required()
	t.Cleanup(func() { logging.SetDefault(logging.New(io.Discard, 0, logging.FormatConsole, false)) })

	logging.From(context.Background()).Info("hello from the test", "token", model.GitHubToken("gho_secret_value"))
	gt.NoError(t, closer.Close())

	gt.NoError(t, w.Close())
	written, err := io.ReadAll(r)
	gt.NoError(t, err).Required()
	gt.Equal(t, string(written), "")

	content, err := os.ReadFile(path)
	gt.NoError(t, err).Required()
	gt.S(t, string(content)).Contains("hello from the test")
	gt.S(t, string(content)).NotContains("gho_secret_value")

	info, err := os.Stat(path)
	gt.NoError(t, err).Required()
	gt.Equal(t, info.Mode().Perm(), os.FileMode(0o600))
}

func TestLoggerRejectsUnknownLevel(t *testing.T) {
	var logger config.Logger
	parse(t, logger.Flags(), "--log-file", filepath.Join(t.TempDir(), "x.log"), "--log-level", "verbose")
	_, err := logger.Configure()
	gt.Error(t, err)
}
