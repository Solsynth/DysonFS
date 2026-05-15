package grpcsvc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"
)

type fileServiceServer struct {
	gen.UnimplementedDyFileServiceServer
	files *service.FileService
}

func Register(s *grpc.Server, files *service.FileService) {
	gen.RegisterDyFileServiceServer(s, &fileServiceServer{files: files})
}

func (s *fileServiceServer) GetFile(ctx context.Context, req *gen.DyGetFileRequest) (*gen.DyCloudFile, error) {
	_, _ = ctx, req
	return &gen.DyCloudFile{}, status.Error(codes.NotFound, "file not found")
}

func (s *fileServiceServer) GetFileBatch(context.Context, *gen.DyGetFileBatchRequest) (*gen.DyGetFileBatchResponse, error) {
	return &gen.DyGetFileBatchResponse{}, nil
}

func (s *fileServiceServer) UpdateFile(context.Context, *gen.DyUpdateFileRequest) (*gen.DyCloudFile, error) {
	return &gen.DyCloudFile{}, nil
}

func (s *fileServiceServer) DeleteFile(context.Context, *gen.DyDeleteFileRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *fileServiceServer) PurgeCache(context.Context, *gen.DyPurgeCacheRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *fileServiceServer) SetFilePublic(context.Context, *gen.DySetFilePublicRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *fileServiceServer) UnsetFilePublic(context.Context, *gen.DyUnsetFilePublicRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
