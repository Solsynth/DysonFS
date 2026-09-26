package grpcsvc

import (
	"context"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type quotaServiceServer struct {
	gen.UnimplementedDyQuotaServiceServer
	quota *service.QuotaService
}

// GetUsedQuota reports the bytes charged to a workspace's storage quota. For an
// individual (personal) workspace that is the workspace's files plus the owner's
// personal files, because both share the owner's account quota; organization
// workspaces report their own files only. This is the same pool the upload
// paths enforce, so Valve's aggregate snapshot matches the limit it publishes.
func (s *quotaServiceServer) GetUsedQuota(ctx context.Context, req *gen.DyGetUsedQuotaRequest) (*gen.DyGetUsedQuotaResponse, error) {
	workspaceID := strings.TrimSpace(req.GetWorkspaceId())
	if _, err := uuid.Parse(workspaceID); err != nil {
		return nil, status.Error(codes.InvalidArgument, "workspace_id must be a valid UUID")
	}
	if s.quota == nil {
		return nil, status.Error(codes.FailedPrecondition, "quota service is not configured")
	}

	usedBytes, err := s.quota.ChargedWorkspaceBytes(ctx, workspaceID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "calculate workspace storage usage: %v", err)
	}

	return &gen.DyGetUsedQuotaResponse{UsedBytes: usedBytes}, nil
}

func registerQuotaService(s *grpc.Server, quota *service.QuotaService) {
	gen.RegisterDyQuotaServiceServer(s, &quotaServiceServer{quota: quota})
}
