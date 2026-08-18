package grpcsvc

import (
	"context"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type quotaServiceServer struct {
	gen.UnimplementedDyQuotaServiceServer
	files *service.FileService
}

func (s *quotaServiceServer) GetUsedQuota(ctx context.Context, req *gen.DyGetUsedQuotaRequest) (*gen.DyGetUsedQuotaResponse, error) {
	workspaceID := strings.TrimSpace(req.GetWorkspaceId())
	if _, err := uuid.Parse(workspaceID); err != nil {
		return nil, status.Error(codes.InvalidArgument, "workspace_id must be a valid UUID")
	}
	if s.files == nil || s.files.DB() == nil {
		return nil, status.Error(codes.FailedPrecondition, "file service is not configured")
	}

	var usedBytes int64
	if err := s.files.DB().DB.WithContext(ctx).
		Model(&database.CloudFile{}).
		Select("COALESCE(SUM(file_objects.size), 0)").
		Joins("JOIN file_objects ON file_objects.id = cloud_files.object_id AND file_objects.deleted_at IS NULL").
		Where("cloud_files.workspace_id = ? AND cloud_files.deleted_at IS NULL", workspaceID).
		Scan(&usedBytes).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "calculate workspace storage usage: %v", err)
	}

	return &gen.DyGetUsedQuotaResponse{UsedBytes: usedBytes}, nil
}

func registerQuotaService(s *grpc.Server, files *service.FileService) {
	gen.RegisterDyQuotaServiceServer(s, &quotaServiceServer{files: files})
}
