package grpcsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const defaultMultipartChunkSize int64 = 5 * 1024 * 1024

type normalizedUploadOptions struct {
	accountID       uuid.UUID
	workspaceID     *string
	fileName        string
	fileSize        int64
	contentType     string
	hash            *string
	description     *string
	indexed         bool
	poolID          *string
	expiredAt       *time.Time
	parentID        *string
	overwriteID     *string
	fastMode        bool
	usage           *string
	applicationType *string
}

func (s *fileServiceServer) UploadFile(stream grpc.ClientStreamingServer[gen.DyUploadFileRequest, gen.DyCloudFile]) error {
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "upload options are required")
		}
		return err
	}
	optionsMessage := first.GetOptions()
	if optionsMessage == nil {
		return status.Error(codes.InvalidArgument, "the first message must contain upload options")
	}
	options, err := s.prepareUpload(stream.Context(), optionsMessage)
	if err != nil {
		return err
	}

	tempPath, cleanup, err := s.createUploadTempFile()
	if err != nil {
		return uploadStatus(err)
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			cleanup()
		}
	}()

	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return uploadStatus(err)
	}
	hasher := sha256.New()
	var received int64
	for {
		request, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			_ = file.Close()
			return recvErr
		}
		if request.GetOptions() != nil {
			_ = file.Close()
			return status.Error(codes.InvalidArgument, "upload options may only be sent once")
		}
		dataPayload, ok := request.GetPayload().(*gen.DyUploadFileRequest_Data)
		if !ok {
			_ = file.Close()
			return status.Error(codes.InvalidArgument, "upload data is required")
		}
		if int64(len(dataPayload.Data)) > options.fileSize-received {
			_ = file.Close()
			return status.Errorf(codes.InvalidArgument, "received more than declared file_size %d", options.fileSize)
		}
		if len(dataPayload.Data) == 0 {
			continue
		}
		written, writeErr := file.Write(dataPayload.Data)
		if writeErr != nil {
			_ = file.Close()
			return uploadStatus(writeErr)
		}
		_, _ = hasher.Write(dataPayload.Data[:written])
		received += int64(written)
		if written != len(dataPayload.Data) {
			_ = file.Close()
			return status.Error(codes.Internal, "short write while staging upload")
		}
	}
	if err := file.Close(); err != nil {
		return uploadStatus(err)
	}
	if received != options.fileSize {
		return status.Errorf(codes.InvalidArgument, "received %d bytes, want %d", received, options.fileSize)
	}

	info := service.NewStagedFileInfo(tempPath, options.contentType, received, hex.EncodeToString(hasher.Sum(nil)))
	created, err := s.persistStagedUpload(stream.Context(), options, tempPath, info, "")
	if err != nil {
		return err
	}
	cleanupTemp = false
	return stream.SendAndClose(toProtoCloudFile(created))
}

func (s *fileServiceServer) CreateMultipartUpload(ctx context.Context, req *gen.DyCreateMultipartUploadRequest) (*gen.DyCreateMultipartUploadResponse, error) {
	if req == nil || req.GetOptions() == nil {
		return nil, status.Error(codes.InvalidArgument, "upload options are required")
	}
	options, err := s.prepareUpload(ctx, req.GetOptions())
	if err != nil {
		return nil, err
	}
	if s.tasks == nil {
		return nil, status.Error(codes.FailedPrecondition, "upload task service is not configured")
	}
	chunkSize := req.GetChunkSize()
	if chunkSize <= 0 {
		chunkSize = defaultMultipartChunkSize
	}
	chunks := (options.fileSize + chunkSize - 1) / chunkSize
	if chunks > int64(^uint32(0)>>1) {
		return nil, status.Error(codes.InvalidArgument, "multipart upload has too many parts")
	}
	payload := &database.PersistentTask{
		Description:     options.description,
		Hash:            options.hash,
		ExpiredAt:       options.expiredAt,
		Usage:           options.usage,
		ParentID:        options.parentID,
		OverwriteID:     options.overwriteID,
		FastMode:        options.fastMode,
		ApplicationType: options.applicationType,
		Indexed:         options.indexed,
		WorkspaceID:     options.workspaceID,
	}
	task, err := s.tasks.CreateUploadTask(
		options.accountID,
		options.fileName,
		payload,
		options.fileSize,
		s.files.ResolvedPoolID(options.poolID),
		options.fileName,
		options.contentType,
		chunkSize,
		int(chunks),
	)
	if err != nil {
		return nil, uploadStatus(err)
	}
	return &gen.DyCreateMultipartUploadResponse{
		UploadId:   task.TaskID,
		ChunkSize:  task.ChunkSize,
		PartsCount: int32(task.ChunksCount),
	}, nil
}

func (s *fileServiceServer) UploadMultipartPart(stream grpc.ClientStreamingServer[gen.DyUploadMultipartPartRequest, gen.DyUploadMultipartPartResponse]) error {
	if s.tasks == nil {
		return status.Error(codes.FailedPrecondition, "upload task service is not configured")
	}
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "multipart part header is required")
		}
		return err
	}
	header := first.GetHeader()
	if header == nil {
		return status.Error(codes.InvalidArgument, "the first message must contain a multipart part header")
	}
	uploadID := strings.TrimSpace(header.GetUploadId())
	if uploadID == "" {
		return status.Error(codes.InvalidArgument, "upload_id is required")
	}
	partIndex := int(header.GetPartIndex())
	if partIndex < 0 {
		return status.Error(codes.InvalidArgument, "part_index must not be negative")
	}
	task, err := s.tasks.GetUploadTask(uploadID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "multipart upload not found")
	}
	if err != nil {
		return uploadStatus(err)
	}
	if task.Type != "file.upload" {
		return status.Error(codes.InvalidArgument, "upload_id does not identify a multipart upload")
	}
	if err := s.requireUploadPermission(stream.Context(), task.AccountID); err != nil {
		return err
	}
	if task.Status != "pending" {
		return status.Errorf(codes.FailedPrecondition, "multipart upload is %s", task.Status)
	}
	if task.FileSize == nil || task.ChunkSize <= 0 || partIndex >= task.ChunksCount {
		return status.Error(codes.InvalidArgument, "part_index is out of range")
	}

	expected := task.ChunkSize
	if remaining := *task.FileSize - int64(partIndex)*task.ChunkSize; remaining < expected {
		expected = remaining
	}
	if err := os.MkdirAll(s.tempDir(), 0o755); err != nil {
		return uploadStatus(err)
	}
	partFile, err := os.CreateTemp(s.tempDir(), uploadID+"-part-*")
	if err != nil {
		return uploadStatus(err)
	}
	partPath := partFile.Name()
	defer os.Remove(partPath)

	var received int64
	for {
		request, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			_ = partFile.Close()
			return recvErr
		}
		if request.GetHeader() != nil {
			_ = partFile.Close()
			return status.Error(codes.InvalidArgument, "multipart part header may only be sent once")
		}
		dataPayload, ok := request.GetPayload().(*gen.DyUploadMultipartPartRequest_Data)
		if !ok {
			_ = partFile.Close()
			return status.Error(codes.InvalidArgument, "multipart part data is required")
		}
		if int64(len(dataPayload.Data)) > expected-received {
			_ = partFile.Close()
			return status.Errorf(codes.InvalidArgument, "part contains more than %d bytes", expected)
		}
		written, writeErr := partFile.Write(dataPayload.Data)
		if writeErr != nil {
			_ = partFile.Close()
			return uploadStatus(writeErr)
		}
		received += int64(written)
		if written != len(dataPayload.Data) {
			_ = partFile.Close()
			return status.Error(codes.Internal, "short write while staging multipart part")
		}
	}
	if received != expected {
		_ = partFile.Close()
		return status.Errorf(codes.InvalidArgument, "part contains %d bytes, want %d", received, expected)
	}
	if _, err := partFile.Seek(0, io.SeekStart); err != nil {
		_ = partFile.Close()
		return uploadStatus(err)
	}
	if _, err := service.WriteUploadChunk(s.tempDir(), uploadID, partIndex, task.ChunkSize, *task.FileSize, partFile); err != nil {
		_ = partFile.Close()
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if err := partFile.Close(); err != nil {
		return uploadStatus(err)
	}
	if err := s.tasks.UpdateUploadedChunk(uploadID, partIndex); err != nil {
		return uploadStatus(err)
	}
	return stream.SendAndClose(&gen.DyUploadMultipartPartResponse{BytesReceived: received})
}

func (s *fileServiceServer) CompleteMultipartUpload(ctx context.Context, req *gen.DyCompleteMultipartUploadRequest) (*gen.DyCloudFile, error) {
	if s.tasks == nil {
		return nil, status.Error(codes.FailedPrecondition, "upload task service is not configured")
	}
	uploadID := strings.TrimSpace(req.GetUploadId())
	if uploadID == "" {
		return nil, status.Error(codes.InvalidArgument, "upload_id is required")
	}
	task, err := s.tasks.GetUploadTaskWithChunks(uploadID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.Error(codes.NotFound, "multipart upload not found")
	}
	if err != nil {
		return nil, uploadStatus(err)
	}
	if task.Type != "file.upload" {
		return nil, status.Error(codes.InvalidArgument, "upload_id does not identify a multipart upload")
	}
	if err := s.requireUploadPermission(ctx, task.AccountID); err != nil {
		return nil, err
	}
	if task.Status != "pending" {
		return nil, status.Errorf(codes.FailedPrecondition, "multipart upload is %s", task.Status)
	}
	if task.FileSize == nil || task.ChunksCount <= 0 || task.ChunksUploaded != task.ChunksCount {
		return nil, status.Error(codes.FailedPrecondition, "multipart upload is incomplete")
	}
	if task.WorkspaceID != nil {
		if err := s.quota.CheckWorkspaceUploadQuota(ctx, *task.WorkspaceID, task.AccountID.String(), *task.FileSize); err != nil {
			return nil, uploadStatus(err)
		}
	}

	stagedPath := filepath.Join(s.tempDir(), uploadID+".upload")
	stat, err := os.Stat(stagedPath)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "multipart upload source is unavailable")
	}
	if stat.Size() != *task.FileSize {
		return nil, status.Errorf(codes.FailedPrecondition, "multipart upload source contains %d bytes, want %d", stat.Size(), *task.FileSize)
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(stagedPath)
		}
	}()

	options := &normalizedUploadOptions{
		accountID:       task.AccountID,
		workspaceID:     task.WorkspaceID,
		fileName:        stringValue(task.FileName),
		fileSize:        *task.FileSize,
		contentType:     stringValue(task.ContentType),
		hash:            task.Hash,
		description:     task.Description,
		indexed:         task.Indexed,
		poolID:          task.PoolID,
		expiredAt:       task.ExpiredAt,
		parentID:        task.ParentID,
		overwriteID:     task.OverwriteID,
		fastMode:        task.FastMode,
		usage:           task.Usage,
		applicationType: task.ApplicationType,
	}
	created, err := s.persistStagedUpload(ctx, options, stagedPath, nil, uploadID)
	if err != nil {
		return nil, err
	}
	if err := s.tasks.MarkCompleted(uploadID); err != nil {
		return nil, uploadStatus(err)
	}
	cleanupTemp = false
	return toProtoCloudFile(created), nil
}

func (s *fileServiceServer) prepareUpload(ctx context.Context, input *gen.DyFileUploadOptions) (*normalizedUploadOptions, error) {
	if s.files == nil || s.quota == nil {
		return nil, status.Error(codes.FailedPrecondition, "upload services are not configured")
	}
	if s.publisher == nil {
		return nil, status.Error(codes.FailedPrecondition, "upload event publisher is not configured")
	}
	accountID, err := uuid.Parse(strings.TrimSpace(input.GetAccountId()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "account_id must be a UUID")
	}
	if err := s.requireUploadPermission(ctx, accountID); err != nil {
		return nil, err
	}
	fileName := strings.TrimSpace(input.GetFileName())
	if fileName == "" {
		return nil, status.Error(codes.InvalidArgument, "file_name is required")
	}
	if input.GetFileSize() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "file_size must be greater than zero")
	}
	contentType := strings.TrimSpace(input.GetContentType())
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	options := &normalizedUploadOptions{
		accountID:       accountID,
		workspaceID:     normalizedString(input.WorkspaceId),
		fileName:        fileName,
		fileSize:        input.GetFileSize(),
		contentType:     contentType,
		hash:            normalizedString(input.Hash),
		description:     normalizedString(input.Description),
		indexed:         input.GetIndex(),
		poolID:          normalizedString(input.PoolId),
		parentID:        normalizedString(input.ParentId),
		overwriteID:     normalizedString(input.OverwriteId),
		fastMode:        input.GetFastMode(),
		usage:           normalizedString(input.Usage),
		applicationType: normalizedString(input.ApplicationType),
	}
	if input.GetExpiredAt() != nil {
		if err := input.GetExpiredAt().CheckValid(); err != nil {
			return nil, status.Error(codes.InvalidArgument, "expired_at is invalid")
		}
		expiredAt := input.GetExpiredAt().AsTime()
		options.expiredAt = &expiredAt
	}

	if options.overwriteID != nil {
		target, err := s.files.GetFileInWorkspace(*options.overwriteID, options.workspaceID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "overwrite target not found")
		}
		if err != nil {
			return nil, uploadStatus(err)
		}
		if target.IsFolder {
			return nil, status.Error(codes.InvalidArgument, "cannot overwrite a folder")
		}
		if target.AccountID != options.accountID {
			return nil, status.Error(codes.PermissionDenied, "overwrite target is owned by another account")
		}
		options.fileName = target.Name
		options.description = target.Description
		options.parentID = target.ParentID
		options.expiredAt = target.ExpiredAt
		options.usage = target.Usage
		options.applicationType = target.ApplicationType
		options.indexed = target.Indexed
		options.workspaceID = target.WorkspaceID
	}

	resolvedPoolID := s.files.ResolvedPoolID(options.poolID)
	poolMultiplier := 1.0
	if resolvedPoolID != nil {
		if pool, err := s.files.GetPool(*resolvedPoolID); err == nil && pool.BillingConfig.CostMultiplier != nil && *pool.BillingConfig.CostMultiplier > 0 {
			poolMultiplier = *pool.BillingConfig.CostMultiplier
		}
	}
	if options.workspaceID != nil {
		err = s.quota.CheckWorkspaceUploadQuota(ctx, *options.workspaceID, options.accountID.String(), options.fileSize)
	} else {
		err = s.quota.CheckUploadQuota(&gen.DyAccount{Id: options.accountID.String()}, options.fileSize, poolMultiplier)
	}
	if err != nil {
		return nil, uploadStatus(err)
	}
	access := service.AccessContext{Account: &gen.DyAccount{Id: options.accountID.String()}}
	if err := s.files.ValidatePoolUsage(access, options.poolID, options.fileSize, options.contentType); err != nil {
		if strings.Contains(err.Error(), "access denied") {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return options, nil
}

func (s *fileServiceServer) persistStagedUpload(ctx context.Context, options *normalizedUploadOptions, stagedPath string, info *service.StagedFileInfo, taskID string) (*database.CloudFile, error) {
	var (
		created     *database.CloudFile
		object      *database.FileObject
		analysis    *service.SourceAnalysis
		analysisErr error
		err         error
	)
	if options.fastMode && options.overwriteID != nil {
		analysis, analysisErr = s.files.AnalyzeSourceFile(ctx, stagedPath, options.contentType)
		created, _, err = s.files.FastOverwriteFile(*options.overwriteID, stagedPath, analysis)
		if err != nil {
			return nil, uploadStatus(err)
		}
		if created != nil {
			object = created.Object
		}
	}
	if created == nil {
		if info == nil {
			info, err = service.InspectStagedFile(stagedPath, options.contentType)
			if err != nil {
				return nil, uploadStatus(err)
			}
		}
		type analysisResult struct {
			value *service.SourceAnalysis
			err   error
		}
		type storageResult struct {
			key string
			err error
		}
		analysisCh := make(chan analysisResult, 1)
		storageCh := make(chan storageResult, 1)
		go func() {
			value, err := s.files.AnalyzeSourceFile(ctx, stagedPath, info.ContentType)
			analysisCh <- analysisResult{value: value, err: err}
		}()
		go func() {
			key, err := s.files.UploadStagedFile(ctx, stagedPath, info)
			storageCh <- storageResult{key: key, err: err}
		}()
		analyzed := <-analysisCh
		stored := <-storageCh
		analysis, analysisErr = analyzed.value, analyzed.err
		if stored.err != nil {
			return nil, uploadStatus(stored.err)
		}
		object, err = s.files.CreateUploadedObject(stored.key, info, analysis)
		if err != nil {
			_ = s.files.Storage().Delete(context.Background(), stored.key)
			return nil, uploadStatus(err)
		}
		storageKey := &object.ID
		if options.overwriteID != nil {
			created, err = s.files.OverwriteFile(*options.overwriteID, object.ID, storageKey)
		} else {
			created, err = s.files.CreateWorkspaceUploadedFile(
				options.accountID,
				options.workspaceID,
				options.fileName,
				options.description,
				options.hash,
				options.expiredAt,
				options.usage,
				options.parentID,
				object.ID,
				options.poolID,
				options.applicationType,
				storageKey,
				options.indexed,
			)
		}
		if err != nil {
			return nil, uploadStatus(err)
		}
	}
	if analysisErr != nil {
		logging.Log.Warn().Err(analysisErr).Str("fileId", created.ID).Msg("failed to analyze gRPC upload")
	}
	if created.Object == nil {
		created, err = s.files.GetFileInWorkspace(created.ID, options.workspaceID)
		if err != nil {
			return nil, uploadStatus(err)
		}
	}
	if s.publisher == nil {
		return nil, status.Error(codes.FailedPrecondition, "upload event publisher is not configured")
	}
	contentType := options.contentType
	if object != nil && strings.TrimSpace(object.MimeType) != "" {
		contentType = object.MimeType
	}
	storageKey := ""
	if created.Object != nil && created.Object.StorageKey != nil {
		storageKey = strings.TrimSpace(*created.Object.StorageKey)
	} else if created.StorageKey != nil {
		storageKey = strings.TrimSpace(*created.StorageKey)
	} else if created.ObjectID != nil {
		storageKey = strings.TrimSpace(*created.ObjectID)
	}
	if err := s.publisher.PublishFileUploaded(ctx, eventbus.FileUploadedEvent{
		FileID:             created.ID,
		TaskID:             taskID,
		ContentType:        contentType,
		StorageKey:         storageKey,
		ProcessingFilePath: stagedPath,
		IsTempFile:         true,
	}); err != nil {
		return nil, uploadStatus(err)
	}
	return created, nil
}

func (s *fileServiceServer) createUploadTempFile() (string, func(), error) {
	if err := os.MkdirAll(s.tempDir(), 0o755); err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp(s.tempDir(), "grpc-upload-*")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func (s *fileServiceServer) tempDir() string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.Storage.TempDir) != "" {
		return s.cfg.Storage.TempDir
	}
	return os.TempDir()
}

func normalizedString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func uploadStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, service.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, service.ErrQuotaExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, fmt.Sprintf("upload failed: %v", err))
	}
}

func (s *fileServiceServer) requireUploadPermission(ctx context.Context, accountID uuid.UUID) error {
	if s.files == nil {
		return status.Error(codes.FailedPrecondition, "file service is not configured")
	}
	return uploadStatus(s.files.RequireAccountPermission(ctx, accountID.String(), service.PermissionFilesUpload))
}
