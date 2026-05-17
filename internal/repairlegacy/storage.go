package repairlegacy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/storage"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Options struct {
	DryRun          bool
	BatchSize       int
	PreviewCount    int
	Limit           int
	ContinueOnError bool
	OnlyDerived     bool
	OnlyOriginal    bool
}

type Repairer struct {
	src  *gorm.DB
	dst  *database.DB
	stor storage.Backend
	op   Options
}

type Summary struct {
	Scanned              int
	Candidates           int
	Matched              int
	Verified             int
	Updated              int
	AlreadyCorrect       int
	SkippedMissingHash   int
	SkippedMissingLegacy int
	SkippedMissingRemote int
	SkippedAmbiguous     int
	SkippedConflict      int
	Failed               int
}

type Candidate struct {
	FileID           string
	ObjectID         string
	Hash             string
	Name             string
	ApplicationType  string
	CurrentFileKey   string
	CurrentObjectKey string
	LegacyFileID     string
	LegacyObjectID   string
	LegacyKey        string
	Reason           string
}

type Preview struct {
	Candidate Candidate
	Status    string
	Detail    string
}

type legacyMatch struct {
	LegacyFileID   string
	LegacyObjectID *string
	LegacyKey      string
	CreatedAtRank  int
}

type repairRecord struct {
	FileID           string
	Name             string
	ObjectID         *string
	ApplicationType  *string
	CurrentFileKey   *string
	CurrentObjectKey *string
	Hash             string
}

type legacyFileRow struct {
	ID              string  `gorm:"column:id"`
	ObjectID        *string `gorm:"column:object_id"`
	StorageID       *string `gorm:"column:storage_id"`
	Hash            *string `gorm:"column:hash"`
	ApplicationType *string `gorm:"column:application_type"`
}

func Open(targetDSN, sourceDSN string, stor storage.Backend, op Options) (*Repairer, error) {
	if strings.TrimSpace(targetDSN) == "" {
		return nil, fmt.Errorf("target dsn is required")
	}
	if strings.TrimSpace(sourceDSN) == "" {
		return nil, fmt.Errorf("legacy dsn is required")
	}
	if stor == nil {
		return nil, fmt.Errorf("storage backend is required")
	}
	src, err := gorm.Open(postgres.Open(sourceDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, err
	}
	dst, err := gorm.Open(postgres.Open(targetDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, err
	}
	if op.BatchSize <= 0 {
		op.BatchSize = 500
	}
	if op.PreviewCount < 0 {
		op.PreviewCount = 0
	}
	if op.OnlyDerived && op.OnlyOriginal {
		return nil, fmt.Errorf("only-derived and only-original cannot both be set")
	}
	return &Repairer{src: src, dst: &database.DB{DB: dst}, stor: stor, op: op}, nil
}

func (r *Repairer) Preview(ctx context.Context) ([]Preview, Summary, error) {
	return r.run(ctx, false)
}

func (r *Repairer) Run(ctx context.Context) ([]Preview, Summary, error) {
	return r.run(ctx, true)
}

func (r *Repairer) run(ctx context.Context, apply bool) ([]Preview, Summary, error) {
	var summary Summary
	if r == nil {
		return nil, summary, fmt.Errorf("repairer is nil")
	}
	records, err := r.loadCandidates()
	if err != nil {
		return nil, summary, err
	}
	summary.Scanned = len(records)
	previews := make([]Preview, 0)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return previews, summary, err
		}
		preview, updated, evalErr := r.evaluateCandidate(ctx, record, apply && !r.op.DryRun)
		if preview.Status != "" {
			summary.Candidates++
			if len(previews) < r.op.PreviewCount {
				previews = append(previews, preview)
			}
		}
		switch preview.Status {
		case "missing-hash":
			summary.SkippedMissingHash++
		case "missing-legacy":
			summary.SkippedMissingLegacy++
		case "missing-remote":
			summary.SkippedMissingRemote++
		case "ambiguous":
			summary.SkippedAmbiguous++
		case "conflict":
			summary.SkippedConflict++
		case "already-correct":
			summary.AlreadyCorrect++
		case "verified":
			summary.Matched++
			summary.Verified++
			if updated {
				summary.Updated++
			}
		}
		if evalErr != nil {
			summary.Failed++
			if !r.op.ContinueOnError {
				return previews, summary, evalErr
			}
		}
	}
	return previews, summary, nil
}

func (r *Repairer) loadCandidates() ([]repairRecord, error) {
	query := r.dst.Model(&database.CloudFile{}).
		Select("cloud_files.id AS file_id, cloud_files.name, cloud_files.object_id, cloud_files.application_type, cloud_files.storage_key AS current_file_key, file_objects.storage_key AS current_object_key, file_objects.hash").
		Joins("JOIN file_objects ON file_objects.id = cloud_files.object_id").
		Where("cloud_files.deleted_at IS NULL").
		Where("file_objects.deleted_at IS NULL").
		Where("cloud_files.object_id IS NOT NULL")
	if r.op.OnlyDerived {
		query = query.Where("cloud_files.application_type IN ?", []string{"system.thumbnail", "system.compression.low"})
	} else if r.op.OnlyOriginal {
		query = query.Where("cloud_files.application_type IS NULL OR cloud_files.application_type = ''")
	}
	query = query.Where(`(
		cloud_files.storage_key = cloud_files.object_id OR
		file_objects.storage_key = cloud_files.object_id OR
		cloud_files.storage_key = cloud_files.object_id || '.thumbnail' OR
		file_objects.storage_key = cloud_files.object_id || '.thumbnail' OR
		cloud_files.storage_key = cloud_files.object_id || '.compressed' OR
		file_objects.storage_key = cloud_files.object_id || '.compressed'
	)`)
	query = query.Order("cloud_files.created_at asc")
	if r.op.Limit > 0 {
		query = query.Limit(r.op.Limit)
	}
	var rows []repairRecord
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *Repairer) evaluateCandidate(ctx context.Context, record repairRecord, apply bool) (Preview, bool, error) {
	preview := Preview{Candidate: Candidate{FileID: record.FileID, Name: record.Name, Hash: record.Hash, ApplicationType: normalizeString(record.ApplicationType), CurrentFileKey: normalizeString(record.CurrentFileKey), CurrentObjectKey: normalizeString(record.CurrentObjectKey)}}
	if record.ObjectID != nil {
		preview.Candidate.ObjectID = *record.ObjectID
	}
	if strings.TrimSpace(record.Hash) == "" {
		preview.Status = "missing-hash"
		preview.Detail = "new file object hash is empty"
		preview.Candidate.Reason = preview.Detail
		return preview, false, nil
	}
	match, status, detail, err := r.matchLegacy(record)
	if err != nil {
		preview.Status = status
		preview.Detail = detail
		preview.Candidate.Reason = detail
		return preview, false, err
	}
	preview.Status = status
	preview.Detail = detail
	preview.Candidate.Reason = detail
	if match == nil {
		return preview, false, nil
	}
	preview.Candidate.LegacyFileID = match.LegacyFileID
	preview.Candidate.LegacyObjectID = normalizeString(match.LegacyObjectID)
	preview.Candidate.LegacyKey = match.LegacyKey
	legacyExists, err := r.keyExists(ctx, match.LegacyKey)
	if err != nil {
		return preview, false, err
	}
	if !legacyExists {
		preview.Status = "missing-remote"
		preview.Detail = "legacy key not found in storage"
		preview.Candidate.Reason = preview.Detail
		return preview, false, nil
	}
	currentKeys := collectKeys(record.CurrentFileKey, record.CurrentObjectKey)
	if len(currentKeys) == 1 && currentKeys[0] == match.LegacyKey {
		preview.Status = "already-correct"
		preview.Detail = "db already points to the verified legacy key"
		preview.Candidate.Reason = preview.Detail
		return preview, false, nil
	}
	for _, key := range currentKeys {
		if key == match.LegacyKey {
			preview.Status = "already-correct"
			preview.Detail = "db already points to the verified legacy key"
			preview.Candidate.Reason = preview.Detail
			return preview, false, nil
		}
	}
	hasReachableCurrent := false
	for _, key := range currentKeys {
		ok, statErr := r.keyExists(ctx, key)
		if statErr != nil {
			return preview, false, statErr
		}
		if ok {
			hasReachableCurrent = true
			break
		}
	}
	if hasReachableCurrent {
		preview.Status = "conflict"
		preview.Detail = "current key also exists in storage; skipping automatic overwrite"
		preview.Candidate.Reason = preview.Detail
		return preview, false, nil
	}
	preview.Status = "verified"
	preview.Detail = "legacy key verified in storage and current key missing"
	preview.Candidate.Reason = preview.Detail
	if !apply {
		return preview, false, nil
	}
	if err := r.applyFix(record.FileID, record.ObjectID, match.LegacyKey); err != nil {
		return preview, false, err
	}
	return preview, true, nil
}

func (r *Repairer) matchLegacy(record repairRecord) (*legacyMatch, string, string, error) {
	var rows []legacyFileRow
	query := r.src.Table("files AS f").
		Select("f.id, f.object_id, f.storage_id, COALESCE(f.hash, fo.hash) AS hash, f.application_type").
		Joins("LEFT JOIN file_objects fo ON fo.id = f.object_id").
		Where("COALESCE(f.hash, fo.hash) = ?", record.Hash)
	appType := normalizeString(record.ApplicationType)
	if appType == "" {
		query = query.Where("application_type IS NULL OR application_type = ''")
	} else {
		query = query.Where("application_type = ?", appType)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, "failed", "legacy lookup failed", err
	}
	rows = filterLegacyRows(rows)
	if len(rows) == 0 {
		return nil, "missing-legacy", "no legacy file matched hash and application type", nil
	}
	matchMap := make(map[string]legacyMatch)
	for idx, row := range rows {
		if row.StorageID == nil || strings.TrimSpace(*row.StorageID) == "" {
			continue
		}
		key := strings.TrimSpace(*row.StorageID)
		if _, ok := matchMap[key]; ok {
			continue
		}
		matchMap[key] = legacyMatch{LegacyFileID: row.ID, LegacyObjectID: row.ObjectID, LegacyKey: key, CreatedAtRank: idx}
	}
	if len(matchMap) == 0 {
		return nil, "missing-legacy", "legacy rows matched hash but storage_id is empty", nil
	}
	if len(matchMap) > 1 {
		keys := make([]string, 0, len(matchMap))
		for key := range matchMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return nil, "ambiguous", fmt.Sprintf("multiple legacy storage keys matched hash: %s", strings.Join(keys, ", ")), nil
	}
	for _, match := range matchMap {
		return &match, "verified", "legacy hash match found", nil
	}
	return nil, "missing-legacy", "no usable legacy match found", nil
}

func (r *Repairer) applyFix(fileID string, objectID *string, key string) error {
	return r.dst.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.CloudFile{}).Where("id = ?", fileID).Update("storage_key", key).Error; err != nil {
			return err
		}
		if objectID != nil && strings.TrimSpace(*objectID) != "" {
			if err := tx.Model(&database.FileObject{}).Where("id = ?", *objectID).Update("storage_key", key).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repairer) keyExists(ctx context.Context, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, nil
	}
	_, err := r.stor.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if isMissingStorage(err) {
		return false, nil
	}
	return false, err
}

func isMissingStorage(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "not found") || strings.Contains(text, "no such file") || strings.Contains(text, "no such key") || strings.Contains(text, "object does not exist") {
		return true
	}
	return errors.Is(err, gorm.ErrRecordNotFound)
}

func collectKeys(values ...*string) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(values))
	for _, value := range values {
		key := normalizeString(value)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func filterLegacyRows(rows []legacyFileRow) []legacyFileRow {
	filtered := make([]legacyFileRow, 0, len(rows))
	for _, row := range rows {
		if row.Hash == nil || strings.TrimSpace(*row.Hash) == "" {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}
