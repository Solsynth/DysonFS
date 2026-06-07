package grpcsvc

import (
	"context"
	"encoding/json"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func extractSourceMeta(file *database.CloudFile) (width, height int, blurhash string) {
	if file == nil {
		return 0, 0, ""
	}
	if file.Object != nil && len(file.Object.Meta) > 0 {
		var meta map[string]any
		if err := json.Unmarshal(file.Object.Meta, &meta); err == nil {
			if v, ok := meta["width"].(float64); ok {
				width = int(v)
			}
			if v, ok := meta["height"].(float64); ok {
				height = int(v)
			}
			if v, ok := meta["blurhash"].(string); ok {
				blurhash = v
			}
		}
	}
	return width, height, blurhash
}

type fileServiceServer struct {
	gen.UnimplementedDyFileServiceServer
	files *service.FileService
}

func Register(s *grpc.Server, files *service.FileService) {
	gen.RegisterDyFileServiceServer(s, &fileServiceServer{files: files})
}

func (s *fileServiceServer) GetFile(_ context.Context, req *gen.DyGetFileRequest) (*gen.DyCloudFile, error) {
	file, err := s.files.GetFile(req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "file not found")
	}
	return toProtoCloudFile(file), nil
}

func (s *fileServiceServer) GetFileBatch(_ context.Context, req *gen.DyGetFileBatchRequest) (*gen.DyGetFileBatchResponse, error) {
	if len(req.GetIds()) == 0 {
		return &gen.DyGetFileBatchResponse{Files: []*gen.DyCloudFile{}}, nil
	}
	var files []database.CloudFile
	if err := s.files.DB().Preload("Object").Find(&files, "id IN ?", req.GetIds()).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make([]*gen.DyCloudFile, 0, len(files))
	for i := range files {
		out = append(out, toProtoCloudFile(&files[i]))
	}
	return &gen.DyGetFileBatchResponse{Files: out}, nil
}

func (s *fileServiceServer) UpdateFile(_ context.Context, req *gen.DyUpdateFileRequest) (*gen.DyCloudFile, error) {
	if req.GetFile() == nil || req.GetFile().GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "file is required")
	}
	var file database.CloudFile
	if err := s.files.DB().Preload("Object").First(&file, "id = ?", req.GetFile().GetId()).Error; err != nil {
		return nil, status.Error(codes.NotFound, "file not found")
	}
	updates := map[string]any{}
	if maskIncludes(req.GetUpdateMask(), "name") {
		updates["name"] = req.GetFile().GetName()
	}
	if maskIncludes(req.GetUpdateMask(), "parent_id") {
		if parent := req.GetFile().GetParentId(); parent == "" {
			updates["parent_id"] = nil
		} else {
			updates["parent_id"] = parent
		}
	}
	if maskIncludes(req.GetUpdateMask(), "indexed") {
		updates["indexed"] = req.GetFile().GetIndexed()
	}
	if maskIncludes(req.GetUpdateMask(), "is_folder") {
		updates["is_folder"] = req.GetFile().GetIsFolder()
	}
	if maskIncludes(req.GetUpdateMask(), "usage") {
		if usage := req.GetFile().GetUsage(); usage == "" {
			updates["usage"] = nil
		} else {
			updates["usage"] = usage
		}
	}
	if maskIncludes(req.GetUpdateMask(), "application_type") {
		if appType := req.GetFile().GetApplicationType(); appType == "" {
			updates["application_type"] = nil
		} else {
			updates["application_type"] = appType
		}
	}
	if len(updates) > 0 {
		if err := s.files.DB().Model(&database.CloudFile{}).Where("id = ?", file.ID).Updates(updates).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if _, ok := updates["parent_id"]; ok {
			s.files.InvalidateFilePermissionCache(context.Background(), file.ID)
		}
	}
	if maskIncludes(req.GetUpdateMask(), "file_meta") {
		if file.ObjectID == nil {
			return nil, status.Error(codes.InvalidArgument, "file object is required")
		}
		if err := s.files.DB().Model(&database.FileObject{}).Where("id = ?", *file.ObjectID).Update("meta", req.GetFile().GetFileMeta()).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if maskIncludes(req.GetUpdateMask(), "user_meta") {
		if err := s.files.DB().Model(&database.CloudFile{}).Where("id = ?", file.ID).Update("user_meta", req.GetFile().GetUserMeta()).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if maskIncludes(req.GetUpdateMask(), "object") && file.Object != nil && req.GetFile().GetObject() != nil {
		objUpdates := map[string]any{}
		if req.GetFile().GetObject().GetMimeType() != "" {
			objUpdates["mime_type"] = req.GetFile().GetObject().GetMimeType()
		}
		if req.GetFile().GetObject().GetHash() != "" {
			objUpdates["hash"] = req.GetFile().GetObject().GetHash()
		}
		objUpdates["size"] = req.GetFile().GetObject().GetSize()
		objUpdates["has_compression"] = req.GetFile().GetObject().GetHasCompression()
		objUpdates["has_thumbnail"] = req.GetFile().GetObject().GetHasThumbnail()
		if err := s.files.DB().Model(&database.FileObject{}).Where("id = ?", file.Object.ID).Updates(objUpdates).Error; err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	updated, err := s.files.GetFile(file.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toProtoCloudFile(updated), nil
}

func (s *fileServiceServer) DeleteFile(_ context.Context, req *gen.DyDeleteFileRequest) (*emptypb.Empty, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if req.GetPurge() {
		if err := s.files.PurgeFile(req.GetId()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else if err := s.files.DeleteFile(req.GetId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.files.InvalidateFilePermissionCache(context.Background(), req.GetId())
	return &emptypb.Empty{}, nil
}

func (s *fileServiceServer) PurgeCache(_ context.Context, req *gen.DyPurgeCacheRequest) (*emptypb.Empty, error) {
	if req.GetFileId() == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	s.files.InvalidateFilePermissionCache(context.Background(), req.GetFileId())
	return &emptypb.Empty{}, nil
}

func (s *fileServiceServer) SetFilePublic(_ context.Context, req *gen.DySetFilePublicRequest) (*emptypb.Empty, error) {
	if req.GetFileId() == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	if err := s.files.DB().Where("file_id = ? AND permission = ?", req.GetFileId(), "read").Delete(&database.FilePermission{}).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.files.InvalidateFilePermissionCache(context.Background(), req.GetFileId())
	return &emptypb.Empty{}, nil
}

func (s *fileServiceServer) UnsetFilePublic(_ context.Context, req *gen.DyUnsetFilePublicRequest) (*emptypb.Empty, error) {
	if req.GetFileId() == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}
	perm := database.FilePermission{ID: database.NewID(), FileID: req.GetFileId(), SubjectType: "private", SubjectID: "", Permission: "read"}
	if err := s.files.DB().Create(&perm).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.files.InvalidateFilePermissionCache(context.Background(), req.GetFileId())
	return &emptypb.Empty{}, nil
}

func toProtoCloudFile(file *database.CloudFile) *gen.DyCloudFile {
	if file == nil {
		return &gen.DyCloudFile{}
	}
	resp := &gen.DyCloudFile{
		Id:              file.ID,
		Name:            file.Name,
		FileMeta:        file.LegacyFileMeta(),
		UserMeta:        file.UserMeta,
		Indexed:         file.Indexed,
		IsFolder:        file.IsFolder,
		Usage:           file.Usage,
		ApplicationType: file.ApplicationType,
		MimeType:        file.ResponseMimeType(),
		ContentType:     file.ResponseMimeType(),
	}
	if file.Object != nil {
		resp.Hash = file.Object.Hash
		resp.Size = file.Object.Size
		resp.HasCompression = file.Object.HasCompression
		resp.Object = &gen.DyFileObject{Id: file.Object.ID, Size: file.Object.Size, Meta: file.Object.Meta, MimeType: file.Object.MimeType, Hash: file.Object.Hash, HasCompression: file.Object.HasCompression, HasThumbnail: file.Object.HasThumbnail}
	}
	if file.ParentID != nil {
		resp.ParentId = file.ParentID
	}
	if width, height, blurhash := extractSourceMeta(file); width > 0 || height > 0 {
		w := int32(width)
		h := int32(height)
		resp.Width = &w
		resp.Height = &h
		if blurhash != "" {
			resp.Blurhash = &blurhash
		}
	} else if blurhash != "" {
		resp.Blurhash = &blurhash
	}
	return resp
}

func maskIncludes(mask *fieldmaskpb.FieldMask, path string) bool {
	if mask == nil {
		return false
	}
	for _, p := range mask.GetPaths() {
		if strings.EqualFold(p, path) {
			return true
		}
	}
	return false
}
