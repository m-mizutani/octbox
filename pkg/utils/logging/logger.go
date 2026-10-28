package logging

import (
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/fatih/color"
	"github.com/m-mizutani/clog"
	"github.com/m-mizutani/clog/hooks"
	"github.com/m-mizutani/masq"
	"github.com/mattn/go-isatty"

	"github.com/m-mizutani/octbox/pkg/domain/model"
)

type Format int

const (
	FormatConsole Format = iota + 1
	FormatJSON
)

var (
	defaultLogger = slog.Default()
	loggerMutex   sync.Mutex
)

func Default() *slog.Logger {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()
	return defaultLogger
}

func SetDefault(logger *slog.Logger) {
	loggerMutex.Lock()
	defaultLogger = logger
	loggerMutex.Unlock()
}

// isTerminal reports whether w is a TTY so clog emits ANSI colors only when
// output is not written to a pipe or file.
func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// redactOptions is the redaction policy: the `masq:"secret"` struct tag, the
// `secret_` field prefix, the Authorization field name (a header map key
// cannot carry a struct tag), and every model.GitHubToken value.
func redactOptions() []masq.Option {
	return []masq.Option{
		masq.WithTag("secret"),
		masq.WithFieldPrefix("secret_"),
		masq.WithFieldName("Authorization"),
		masq.WithType[model.GitHubToken](),
	}
}

func New(w io.Writer, level slog.Level, format Format, stacktrace bool) *slog.Logger {
	filter := masq.New(redactOptions()...)

	var handler slog.Handler
	switch format {
	case FormatConsole:
		handler = clog.New(
			clog.WithWriter(w),
			clog.WithLevel(level),
			clog.WithReplaceAttr(filter),
			clog.WithAttrHook(hooks.GoErr(hooks.WithStackTrace(stacktrace))),
			clog.WithColor(isTerminal(w)),
			clog.WithColorMap(&clog.ColorMap{
				Level: map[slog.Level]*color.Color{
					slog.LevelDebug: color.New(color.FgGreen, color.Bold),
					slog.LevelInfo:  color.New(color.FgCyan, color.Bold),
					slog.LevelWarn:  color.New(color.FgYellow, color.Bold),
					slog.LevelError: color.New(color.FgRed, color.Bold),
				},
				LevelDefault: color.New(color.FgBlue, color.Bold),
				Time:         color.New(color.FgWhite),
				Message:      color.New(color.FgHiWhite),
				AttrKey:      color.New(color.FgHiCyan),
				AttrValue:    color.New(color.FgHiWhite),
			}),
		)

	case FormatJSON:
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			AddSource:   true,
			Level:       level,
			ReplaceAttr: filter,
		})

	default:
		panic("unsupported log format: " + fmt.Sprintf("%d", format))
	}

	return slog.New(handler)
}
