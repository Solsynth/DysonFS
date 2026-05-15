package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"src.solsynth.dev/sosys/filesystem/internal/app"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
)

func main() {
	mode := flag.String("mode", "master", "run mode: master, worker, storage")
	configPath := flag.String("config", os.Getenv("CONFIG_PATH"), "config file path")
	pretty := flag.Bool("pretty", os.Getenv("ZEROLOG_PRETTY") == "true", "pretty logging")
	flag.Parse()

	logging.Init(*pretty)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logging.Log.Fatal().Err(err).Msg("failed to load config")
	}

	runner, err := app.New(cfg, *mode)
	if err != nil {
		logging.Log.Fatal().Err(err).Str("mode", *mode).Msg("failed to create runtime")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runner.Start(ctx); err != nil {
		logging.Log.Fatal().Err(err).Str("mode", *mode).Msg("runtime failed")
	}

	<-ctx.Done()
	if err := runner.Stop(context.Background()); err != nil {
		logging.Log.Error().Err(err).Msg("shutdown error")
	}

	fmt.Println("shutdown complete")
}
