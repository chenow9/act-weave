package logging

import (
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Level  string
	Format string
	Writer io.Writer
}

func New(config Config) *slog.Logger {
	writer := config.Writer
	if writer == nil {
		writer = os.Stdout
	}

	level := parseLevel(config.Level)
	options := &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.LevelKey {
				attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
			}
			return attr
		},
	}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(config.Format), "text") {
		handler = newPrettyHandler(writer, level)
	} else {
		handler = slog.NewJSONHandler(writer, options)
	}
	return slog.New(handler)
}

func ConfigureDefaults(logger *slog.Logger) {
	if logger == nil {
		return
	}

	slog.SetDefault(logger)
	log.SetFlags(0)
	log.SetOutput(slog.NewLogLogger(logger.Handler(), slog.LevelInfo).Writer())
}

func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
