package logging

import (
	"context"
	"log/slog"
)

type loggerKeyType struct{}

func With(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKeyType{}, logger)
}

func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKeyType{}).(*slog.Logger); ok {
		return logger
	}
	return Default()
}
