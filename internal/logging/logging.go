package logging

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var Log zerolog.Logger

func Init(pretty bool) {
	level := zerolog.InfoLevel
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		if parsed, err := zerolog.ParseLevel(strings.ToLower(envLevel)); err == nil {
			level = parsed
		}
	}

	zerolog.SetGlobalLevel(level)

	if pretty {
		Log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).With().Timestamp().Caller().Logger()
		return
	}

	Log = zerolog.New(os.Stdout).With().Timestamp().Caller().Logger()
}
