package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"src.solsynth.dev/sosys/filesystem/internal/app"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/migratelegacy"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

func main() {
	modeFlag := flag.String("mode", "", "run mode: master, worker, storage, migrate-legacy, reanalyze-missing")
	configPath := flag.String("config", os.Getenv("CONFIG_PATH"), "config file path")
	legacyDSN := flag.String("legacy-dsn", os.Getenv("LEGACY_DATABASE_DSN"), "legacy database dsn for migration")
	dryRun := flag.Bool("dry-run", false, "simulate migration without writing")
	batchSize := flag.Int("batch-size", 500, "migration batch size")
	skipDerived := flag.Bool("skip-derived", false, "skip derived child reconstruction")
	continueOnError := flag.Bool("continue-on-error", false, "continue migration after row errors")
	reanalyzeLimit := flag.Int("reanalyze-limit", 100, "maximum image records to preview and repair")
	previewCount := flag.Int("preview-count", 20, "how many repair candidates to print before confirmation")
	yes := flag.Bool("yes", false, "skip confirmation prompt for repair mode")
	pretty := flag.Bool("pretty", os.Getenv("ZEROLOG_PRETTY") == "true", "pretty logging")
	positionalMode := extractPositionalMode()
	flag.Parse()

	logging.Init(*pretty)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logging.Log.Fatal().Err(err).Msg("failed to load config")
	}

	mode := positionalMode
	if mode == "" {
		mode = *modeFlag
	}
	if mode == "" {
		mode = "master"
	}

	if mode == "migrate-legacy" {
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

	if mode == "reanalyze-missing" {
		if err := runReanalysisCLI(context.Background(), cfg, *reanalyzeLimit, *previewCount, *yes); err != nil {
			logging.Log.Fatal().Err(err).Msg("reanalysis failed")
		}
		return
	}

	runner, err := app.New(cfg, mode)
	if err != nil {
		logging.Log.Fatal().Err(err).Str("mode", mode).Msg("failed to create runtime")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runner.Start(ctx); err != nil {
		logging.Log.Fatal().Err(err).Str("mode", mode).Msg("runtime failed")
	}

	<-ctx.Done()
	if err := runner.Stop(context.Background()); err != nil {
		logging.Log.Error().Err(err).Msg("shutdown error")
	}

	fmt.Println("shutdown complete")
}

func extractPositionalMode() string {
	if len(os.Args) < 2 {
		return ""
	}
	if strings.HasPrefix(os.Args[1], "-") {
		return ""
	}
	mode := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
	return mode
}

func runReanalysisCLI(ctx context.Context, cfg *config.Config, limit, previewCount int, skipConfirm bool) error {
	runner, err := app.New(cfg, "master")
	if err != nil {
		return err
	}
	files := runner.Files()
	candidates, err := files.ListImageReanalysisCandidates(ctx, limit)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("no image records need reanalysis")
		return nil
	}
	if previewCount < 0 {
		previewCount = 0
	}
	if previewCount > len(candidates) {
		previewCount = len(candidates)
	}
	fmt.Printf("reanalysis preview (%d candidates):\n", len(candidates))
	for i := 0; i < previewCount; i++ {
		c := candidates[i]
		fmt.Printf("- %s | %s | size=%d | reason=%s\n", c.FileID, c.Name, c.Size, c.Reason)
	}
	if previewCount < len(candidates) {
		fmt.Printf("... and %d more\n", len(candidates)-previewCount)
	}
	if !skipConfirm && !confirmProceed() {
		fmt.Println("reanalysis cancelled")
		return nil
	}
	if err := runProgressBar(ctx, files, candidates); err != nil {
		return err
	}
	return nil
}

func confirmProceed() bool {
	fmt.Print("Proceed with reanalysis? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func runProgressBar(ctx context.Context, files *service.FileService, candidates []service.ReanalysisCandidate) error {
	total := len(candidates)
	for i, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := files.RepairImageMetadataCandidate(ctx, candidate.FileID); err != nil {
			fmt.Printf("[%d/%d] %s failed: %v\n", i+1, total, candidate.FileID, err)
			continue
		}
		printProgress(i+1, total, candidate.Name)
	}
	fmt.Println()
	return nil
}

func printProgress(current, total int, label string) {
	width := 24
	filled := 0
	if total > 0 {
		filled = current * width / total
	}
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", width-filled)
	fmt.Printf("\r[%s] %d/%d %s", bar, current, total, label)
}
