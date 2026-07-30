package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gen "src.solsynth.dev/sosys/go/proto"
)

const (
	PermissionFilesUpload = "files.upload"
	PermissionFilesManage = "files.manage"
)

var ErrPermissionDenied = errors.New("permission denied")

// PermissionChecker is the minimal Padlock permission-node contract used by
// file operations. Keeping it narrow makes the authorization policy testable
// without a live Padlock service.
type PermissionChecker interface {
	HasPermission(context.Context, string, string) (bool, error)
}

type grpcPermissionChecker struct {
	client gen.DyPermissionServiceClient
}

func (c grpcPermissionChecker) HasPermission(ctx context.Context, accountID, key string) (bool, error) {
	response, err := c.client.HasPermission(ctx, &gen.DyHasPermissionRequest{
		Actor: accountID,
		Key:   key,
	})
	if err != nil {
		return false, err
	}
	return response.GetHasPermission(), nil
}

// SetPermissionChecker configures the global Padlock permission-node checker.
// A nil checker preserves standalone deployments that do not configure Auth.
func (s *FileService) SetPermissionChecker(checker PermissionChecker) {
	s.permissionChecker = checker
}

func (s *FileService) SetPermissionClient(client gen.DyPermissionServiceClient) {
	if client == nil {
		s.SetPermissionChecker(nil)
		return
	}
	s.SetPermissionChecker(grpcPermissionChecker{client: client})
}

// RequireAccountPermission authorizes a global Padlock permission node for an
// account. Superuser bypasses are handled by the HTTP caller to match
// Padlock's LocalPermissionMiddleware behavior.
func (s *FileService) RequireAccountPermission(ctx context.Context, accountID, key string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("%w: account id is required", ErrPermissionDenied)
	}
	if s == nil || s.permissionChecker == nil {
		return nil
	}
	allowed, err := s.permissionChecker.HasPermission(ctx, accountID, key)
	if err != nil {
		return fmt.Errorf("check permission %q: %w", key, err)
	}
	if !allowed {
		return fmt.Errorf("%w: %s is required", ErrPermissionDenied, key)
	}
	return nil
}
