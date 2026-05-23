package sentry

import (
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"

	"github.com/getsentry/sentry-go"
)

func Init(cfg config.SentryConfig) error {
	if cfg.DSN == "" {
		return nil
	}
	return sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.DSN,
		TracesSampleRate: cfg.TracesSampleRate,
		Environment:      cfg.Environment,
		Release:          cfg.Release,
	})
}

func Flush(timeout time.Duration) {
	sentry.Flush(timeout)
}

func CaptureException(err error) {
	sentry.CaptureException(err)
}

func Recover() {
	if r := recover(); r != nil {
		sentry.CurrentHub().Recover(r)
		sentry.Flush(2 * time.Second)
		panic(r)
	}
}
