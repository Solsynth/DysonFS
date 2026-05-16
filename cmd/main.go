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
	"src.solsynth.dev/sosys/filesystem/internal/migratelegacy"
)

func main() {
	mode := flag.String("mode", "master", "run mode: master, worker, storage")
	configPath := flag.String("config", os.Getenv("CONFIG_PATH"), "config file path")
	legacyDSN := flag.String("legacy-dsn", os.Getenv("LEGACY_DATABASE_DSN"), "legacy database dsn for migration")
	dryRun := flag.Bool("dry-run", false, "simulate migration without writing")
	batchSize := flag.Int("batch-size", 500, "migration batch size")
	skipDerived := flag.Bool("skip-derived", false, "skip derived child reconstruction")
	continueOnError := flag.Bool("continue-on-error", false, "continue migration after row errors")
	pretty := flag.Bool("pretty", os.Getenv("ZEROLOG_PRETTY") == "true", "pretty logging")
	flag.Parse()

	logging.Init(*pretty)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logging.Log.Fatal().Err(err).Msg("failed to load config")
	}

	if *mode == "migrate-legacy" {
		migrator, err := migratelegacy.OpenTargetAndSource(cfg.Database.DSN, *legacyDSN, migratelegacy.Options{
			DryRun:          *dryRun,
			BatchSize:       *batchSize,
			SkipDerived:     *skipDerived,
			ContinueOnError: *continueOnError,
		})
		if err != nil {
			logging.Log.Fatal().Err(err).Msg("failed to create migrator")
		}
		summary, err := migrator.Run(context.Background())
		if err != nil {
			logging.Log.Fatal().Err(err).Msg("legacy migration failed")
		}
		fmt.Printf("migration complete: pools=%d quota=%d objects=%d files=%d perms=%d replicas=%d derived_files=%d derived_objects=%d skipped=%d failed=%d\n", summary.Pools, summary.QuotaRecords, summary.FileObjects, summary.Files, summary.FilePerms, summary.FileReplicas, summary.DerivedFiles, summary.DerivedObjects, summary.Skipped, summary.Failed)
		return
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
