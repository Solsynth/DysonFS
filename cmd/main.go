package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/app"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/migratelegacy"
	"src.solsynth.dev/sosys/filesystem/internal/repairlegacy"
	sentryutil "src.solsynth.dev/sosys/filesystem/internal/sentry"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

func main() {
	modeFlag := flag.String("mode", "", "run mode: master, worker, storage, migrate-legacy, reanalyze-missing, repair-legacy-storage, repair-missing-replicas")
	configPath := flag.String("config", os.Getenv("CONFIG_PATH"), "config file path")
	legacyDSN := flag.String("legacy-dsn", os.Getenv("LEGACY_DATABASE_DSN"), "legacy database dsn for migration")
	dryRun := flag.Bool("dry-run", false, "simulate migration without writing")
	batchSize := flag.Int("batch-size", 500, "migration batch size")
	skipDerived := flag.Bool("skip-derived", false, "skip derived child reconstruction")
	continueOnError := flag.Bool("continue-on-error", false, "continue migration after row errors")
	repairLimit := flag.Int("repair-limit", 0, "maximum suspicious rows to inspect for legacy storage repair")
	onlyDerived := flag.Bool("only-derived", false, "repair derived variants only")
	onlyOriginal := flag.Bool("only-original", false, "repair original files only")
	reanalyzeLimit := flag.Int("reanalyze-limit", 100, "maximum image records to preview and repair")
	reanalyzeFileIDs := flag.String("file-id", "", "comma-separated file ids to reanalyze")
	replicaRepairLimit := flag.Int("replica-repair-limit", 0, "maximum missing replica candidates to inspect")
	previewCount := flag.Int("preview-count", 20, "how many repair candidates to print before confirmation")
	yes := flag.Bool("yes", false, "skip confirmation prompt for repair mode")
	validateSnapshot := flag.String("validate-snapshot", "", "path to remote snapshot file")
	validatePrefix := flag.String("validate-prefix", "", "remote key prefix to validate")
	validateBatch := flag.Int("validate-batch", 500, "db batch size for validation")
	pretty := flag.Bool("pretty", os.Getenv("ZEROLOG_PRETTY") == "true", "pretty logging")
	positionalMode := extractPositionalMode()
	flag.Parse()

	logging.Init(*pretty)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logging.Log.Fatal().Err(err).Msg("failed to load config")
	}

	if err := sentryutil.Init(cfg.Sentry); err != nil {
		logging.Log.Warn().Err(err).Msg("sentry init failed")
	}
	defer sentryutil.Flush(2 * time.Second)
	defer sentryutil.Recover()

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
		if err := runReanalysisCLI(context.Background(), cfg, *reanalyzeLimit, *previewCount, *yes, splitCSV(*reanalyzeFileIDs)); err != nil {
			logging.Log.Fatal().Err(err).Msg("reanalysis failed")
		}
		return
	}

	if mode == "repair-legacy-storage" {
		if err := runLegacyStorageRepairCLI(context.Background(), cfg, *legacyDSN, *dryRun, *batchSize, *previewCount, *repairLimit, *continueOnError, *onlyDerived, *onlyOriginal, *yes); err != nil {
			logging.Log.Fatal().Err(err).Msg("legacy storage repair failed")
		}
		return
	}

	if mode == "repair-missing-replicas" {
		if err := runMissingReplicaRepairCLI(context.Background(), cfg, *replicaRepairLimit, *previewCount, *dryRun, *yes); err != nil {
			logging.Log.Fatal().Err(err).Msg("missing replica repair failed")
		}
		return
	}

	if mode == "validate-storage" {
		if err := runStorageValidationCLI(context.Background(), cfg, *validateSnapshot, *validatePrefix, *validateBatch, *yes); err != nil {
			logging.Log.Fatal().Err(err).Msg("storage validation failed")
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

func runReanalysisCLI(ctx context.Context, cfg *config.Config, limit, previewCount int, skipConfirm bool, fileIDs []string) error {
	runner, err := app.New(cfg, "master")
	if err != nil {
		return err
	}
	files := runner.Files()
	if len(fileIDs) > 0 {
		fmt.Printf("targeted reanalysis for %d file(s):\n", len(fileIDs))
		for _, fileID := range fileIDs {
			fmt.Printf("- %s\n", fileID)
		}
		if !skipConfirm && !confirmProceed() {
			fmt.Println("reanalysis cancelled")
			return nil
		}
		result, err := files.ReanalyzeFiles(ctx, fileIDs)
		if err != nil {
			return err
		}
		fmt.Printf("reanalysis complete: scanned=%d updated=%d failed=%d\n", result.Scanned, result.Updated, result.Failed)
		return nil
	}
	candidates, err := files.ListReanalysisCandidates(ctx, limit)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("no records need reanalysis")
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
		fmt.Printf("- %s | %s | mime=%s | reason=%s\n", c.FileID, c.Name, c.MimeType, c.Reason)
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

func runLegacyStorageRepairCLI(ctx context.Context, cfg *config.Config, legacyDSN string, dryRun bool, batchSize, previewCount, limit int, continueOnError, onlyDerived, onlyOriginal, skipConfirm bool) error {
	runner, err := app.New(cfg, "master")
	if err != nil {
		return err
	}
	repairer, err := repairlegacy.Open(cfg.Database.DSN, legacyDSN, runner.Files().Storage(), repairlegacy.Options{
		DryRun:          dryRun,
		BatchSize:       batchSize,
		PreviewCount:    previewCount,
		Limit:           limit,
		ContinueOnError: continueOnError,
		OnlyDerived:     onlyDerived,
		OnlyOriginal:    onlyOriginal,
	})
	if err != nil {
		return err
	}
	previews, summary, err := repairer.Preview(ctx)
	if err != nil {
		return err
	}
	printLegacyRepairPreview(previews, summary, dryRun)
	if summary.Verified == 0 {
		fmt.Println("no verified legacy storage fixes found")
		return nil
	}
	if dryRun {
		fmt.Println("dry run complete")
		return nil
	}
	if !skipConfirm && !confirmProceedWithMessage("Proceed with legacy storage repair?") {
		fmt.Println("legacy storage repair cancelled")
		return nil
	}
	_, summary, err = repairer.Run(ctx)
	if err != nil {
		return err
	}
	printLegacyRepairSummary(summary)
	return nil
}

func runMissingReplicaRepairCLI(ctx context.Context, cfg *config.Config, limit, previewCount int, dryRun, skipConfirm bool) error {
	runner, err := app.New(cfg, "master")
	if err != nil {
		return err
	}
	files := runner.Files()
	previews, summary, err := files.PreviewMissingReplicas(ctx, limit)
	if err != nil {
		return err
	}
	if previewCount >= 0 && previewCount < len(previews) {
		previews = previews[:previewCount]
	}
	printReplicaRepairPreview(previews, summary, dryRun)
	if summary.Verified == 0 {
		fmt.Println("no verified missing replicas found")
		return nil
	}
	if dryRun {
		fmt.Println("dry run complete")
		return nil
	}
	if !skipConfirm && !confirmProceedWithMessage("Proceed with missing replica repair?") {
		fmt.Println("missing replica repair cancelled")
		return nil
	}
	_, summary, err = files.RepairMissingReplicas(ctx, limit)
	if err != nil {
		return err
	}
	printReplicaRepairSummary(summary)
	return nil
}

func confirmProceed() bool {
	return confirmProceedWithMessage("Proceed with reanalysis?")
}

func confirmProceedWithMessage(message string) bool {
	fmt.Printf("%s [y/N]: ", message)
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
		if err := files.RepairReanalysisCandidate(ctx, candidate.FileID); err != nil {
			fmt.Printf("[%d/%d] %s failed: %v\n", i+1, total, candidate.FileID, err)
			continue
		}
		printProgress(i+1, total, candidate.Name)
	}
	fmt.Println()
	return nil
}

func runStorageValidationCLI(ctx context.Context, cfg *config.Config, snapshotPath, prefix string, batchSize int, skipConfirm bool) error {
	runner, err := app.New(cfg, "master")
	if err != nil {
		return err
	}
	files := runner.Files()
	if batchSize <= 0 {
		batchSize = 500
	}
	if snapshotPath == "" {
		snapshotPath = filepath.Join(os.TempDir(), "dysonfs-remote-snapshot.txt")
	}
	remoteKeys, err := files.Storage().List(ctx, prefix)
	if err != nil {
		return err
	}
	if err := writeLines(snapshotPath, remoteKeys); err != nil {
		return err
	}
	fmt.Printf("remote snapshot written: %s (%d keys)\n", snapshotPath, len(remoteKeys))
	if !skipConfirm && !confirmProceed() {
		fmt.Println("storage validation cancelled")
		return nil
	}
	deleted, err := validateStorageAgainstDB(ctx, files, remoteKeys, batchSize)
	if err != nil {
		return err
	}
	fmt.Printf("validation complete: deleted=%d\n", deleted)
	return nil
}

func printLegacyRepairPreview(previews []repairlegacy.Preview, summary repairlegacy.Summary, dryRun bool) {
	fmt.Printf("legacy storage repair preview: scanned=%d candidates=%d matched=%d verified=%d already_correct=%d missing_hash=%d missing_legacy=%d missing_remote=%d ambiguous=%d conflict=%d failed=%d\n", summary.Scanned, summary.Candidates, summary.Matched, summary.Verified, summary.AlreadyCorrect, summary.SkippedMissingHash, summary.SkippedMissingLegacy, summary.SkippedMissingRemote, summary.SkippedAmbiguous, summary.SkippedConflict, summary.Failed)
	if len(previews) == 0 {
		return
	}
	if dryRun {
		fmt.Println("preview candidates:")
	} else {
		fmt.Println("sample candidates:")
	}
	for _, item := range previews {
		fmt.Printf("- [%s] file=%s object=%s app=%s current_file=%s current_object=%s legacy_key=%s detail=%s\n", item.Status, item.Candidate.FileID, blankAsDash(item.Candidate.ObjectID), blankAsDash(item.Candidate.ApplicationType), blankAsDash(item.Candidate.CurrentFileKey), blankAsDash(item.Candidate.CurrentObjectKey), blankAsDash(item.Candidate.LegacyKey), item.Detail)
	}
}

func printLegacyRepairSummary(summary repairlegacy.Summary) {
	fmt.Printf("legacy storage repair complete: scanned=%d candidates=%d matched=%d verified=%d updated=%d already_correct=%d missing_hash=%d missing_legacy=%d missing_remote=%d ambiguous=%d conflict=%d failed=%d\n", summary.Scanned, summary.Candidates, summary.Matched, summary.Verified, summary.Updated, summary.AlreadyCorrect, summary.SkippedMissingHash, summary.SkippedMissingLegacy, summary.SkippedMissingRemote, summary.SkippedAmbiguous, summary.SkippedConflict, summary.Failed)
}

func printReplicaRepairPreview(previews []service.ReplicaRepairPreview, summary service.ReplicaRepairSummary, dryRun bool) {
	fmt.Printf("missing replica preview: scanned=%d candidates=%d verified=%d already_present=%d missing_pool=%d missing_key=%d missing_remote=%d failed=%d\n", summary.Scanned, summary.Candidates, summary.Verified, summary.AlreadyPresent, summary.MissingPool, summary.MissingKey, summary.MissingRemote, summary.Failed)
	if len(previews) == 0 {
		return
	}
	if dryRun {
		fmt.Println("preview candidates:")
	} else {
		fmt.Println("sample candidates:")
	}
	for _, item := range previews {
		fmt.Printf("- [%s] object=%s file=%s pool=%s key=%s detail=%s\n", item.Status, blankAsDash(item.ObjectID), blankAsDash(item.FileID), blankAsDash(item.PoolID), blankAsDash(item.StorageKey), item.Detail)
	}
}

func printReplicaRepairSummary(summary service.ReplicaRepairSummary) {
	fmt.Printf("missing replica repair complete: scanned=%d candidates=%d verified=%d created=%d already_present=%d missing_pool=%d missing_key=%d missing_remote=%d failed=%d\n", summary.Scanned, summary.Candidates, summary.Verified, summary.Created, summary.AlreadyPresent, summary.MissingPool, summary.MissingKey, summary.MissingRemote, summary.Failed)
}

func blankAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func writeLines(path string, lines []string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func validateStorageAgainstDB(ctx context.Context, files *service.FileService, remoteKeys []string, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	remoteSet := make(map[string]struct{}, len(remoteKeys))
	for _, key := range remoteKeys {
		remoteSet[key] = struct{}{}
	}
	var allKeys []string
	if err := files.DB().Unscoped().Model(&database.FileObject{}).Where("storage_key IS NOT NULL AND storage_key <> ''").Pluck("storage_key", &allKeys).Error; err != nil {
		return 0, err
	}
	for _, key := range allKeys {
		delete(remoteSet, key)
	}
	deleted := 0
	for key := range remoteSet {
		if err := files.Storage().Delete(ctx, key); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
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
