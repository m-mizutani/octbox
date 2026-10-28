package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/utils/logging"
)

type Logger struct {
	level string
	file  string
}

func (x *Logger) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "log-level",
			Category:    "Logging",
			Usage:       "Log level [debug|info|warn|error]",
			Value:       "info",
			Sources:     cli.EnvVars("OCTBOX_LOG_LEVEL"),
			Destination: &x.level,
		},
		&cli.StringFlag{
			Name:        "log-file",
			Category:    "Logging",
			Usage:       "Path of the log file (appended)",
			Value:       defaultPath("octbox.log"),
			Sources:     cli.EnvVars("OCTBOX_LOG_FILE"),
			Destination: &x.file,
		},
	}
}

// Configure installs the default logger writing to the log file. Logs never
// go to stdout, which the full-screen inbox uses. The caller closes the
// returned file.
func (x *Logger) Configure() (io.Closer, error) {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	level, ok := levels[x.level]
	if !ok {
		return nil, goerr.New("invalid --log-level", goerr.V("log_level", x.level))
	}
	if x.file == "" {
		return nil, errNoHome
	}

	if err := os.MkdirAll(filepath.Dir(x.file), 0o700); err != nil {
		return nil, goerr.Wrap(err, "failed to create the log directory", goerr.V("path", x.file))
	}
	f, err := os.OpenFile(x.file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to open the log file", goerr.V("path", x.file))
	}

	logging.SetDefault(logging.New(f, level, logging.FormatConsole, true))
	return f, nil
}
