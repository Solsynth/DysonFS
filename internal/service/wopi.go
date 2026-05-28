package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
)

var (
	ErrWOPIDisabled             = errors.New("wopi is disabled")
	ErrWOPIProofUnsupported     = errors.New("wopi proof validation is not implemented")
	ErrWOPIUnauthorized         = errors.New("wopi unauthorized")
	ErrWOPIUnsupportedFile      = errors.New("file is not supported by collabora")
	ErrWOPIConflict             = errors.New("wopi lock conflict")
	ErrWOPIInvalidLockOperation = errors.New("invalid wopi lock operation")
)

type WOPIService struct {
	cfg   config.WOPIConfig
	files *FileService
	http  *http.Client

	mu             sync.Mutex
	discovery      *wopiDiscoveryData
	discoveryUntil time.Time
}

type WOPISession struct {
	ActionURL  string            `json:"actionUrl"`
	Action     string            `json:"action"`
	Method     string            `json:"method"`
	FormFields map[string]string `json:"formFields"`
	WOPISrc    string            `json:"wopiSrc"`
	ExpiresAt  time.Time         `json:"expiresAt"`
}

type WOPITokenClaims struct {
	FileID     string `json:"fileId"`
	AccountID  string `json:"accountId"`
	SessionID  string `json:"sessionId"`
	Permission string `json:"permission"`
	ExpiresAt  int64  `json:"expiresAt"`
}

type WOPICheckFileInfo struct {
	BaseFileName     string `json:"BaseFileName"`
	Size             int64  `json:"Size"`
	Version          string `json:"Version"`
	UserID           string `json:"UserId"`
	UserFriendlyName string `json:"UserFriendlyName"`
	UserCanWrite     bool   `json:"UserCanWrite"`
	ReadOnly         bool   `json:"ReadOnly"`
	SupportsLocks    bool   `json:"SupportsLocks"`
	SupportsGetLock  bool   `json:"SupportsGetLock"`
	SupportsUpdate   bool   `json:"SupportsUpdate"`
	SupportsRename   bool   `json:"SupportsRename"`
	UserCanRename    bool   `json:"UserCanRename"`
}

type WOPILockResult struct {
	CurrentLock string
}

type wopiTokenEnvelope struct {
	Payload string `json:"payload"`
	Sig     string `json:"sig"`
}

type wopiDiscoveryXML struct {
	NetZones []wopiDiscoveryNetZone `xml:"net-zone"`
}

type wopiDiscoveryNetZone struct {
	Apps []wopiDiscoveryApp `xml:"app"`
}

type wopiDiscoveryApp struct {
	Name    string                `xml:"name,attr"`
	Actions []wopiDiscoveryAction `xml:"action"`
}

type wopiDiscoveryAction struct {
	Ext    string `xml:"ext,attr"`
	Name   string `xml:"name,attr"`
	URLSrc string `xml:"urlsrc,attr"`
}

type wopiDiscoveryData struct {
	Actions map[string]map[string]string
}

func NewWOPIService(cfg config.WOPIConfig, files *FileService) (*WOPIService, error) {
	if cfg.RequireProof {
		return nil, ErrWOPIProofUnsupported
	}
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = 15 * time.Minute
	}
	if cfg.ProofCacheTTL <= 0 {
		cfg.ProofCacheTTL = time.Hour
	}
	return &WOPIService{
		cfg:   cfg,
		files: files,
		http:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (s *WOPIService) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

func (s *WOPIService) SetHTTPClient(client *http.Client) {
	if s != nil && client != nil {
		s.http = client
	}
}

func (s *WOPIService) CreateSession(ctx context.Context, fileID string, account *gen.DyAccount, session *gen.DyAuthSession) (*WOPISession, error) {
	if !s.Enabled() {
		return nil, ErrWOPIDisabled
	}
	file, err := s.files.GetFile(strings.TrimSpace(fileID))
	if err != nil {
		return nil, err
	}
	if file.IsFolder {
		return nil, ErrWOPIUnsupportedFile
	}
	canRead := s.files.CanAccessFile(account, session, file, "read")
	if !canRead {
		return nil, ErrWOPIUnauthorized
	}
	permission := "view"
	actionName := "view"
	if s.files.CanAccessFile(account, session, file, "write") {
		permission = "edit"
		actionName = "edit"
	}
	actionURL, err := s.actionURL(ctx, file.Name, s.wopiSrc(file.ID), actionName)
	if err != nil && actionName == "edit" {
		permission = "view"
		actionName = "view"
		actionURL, err = s.actionURL(ctx, file.Name, s.wopiSrc(file.ID), actionName)
	}
	if err != nil {
		return nil, err
	}
	token, expiresAt, err := s.signToken(WOPITokenClaims{
		FileID:     file.ID,
		AccountID:  account.GetId(),
		SessionID:  session.GetId(),
		Permission: permission,
	})
	if err != nil {
		return nil, err
	}
	return &WOPISession{
		ActionURL: actionURL,
		Action:    actionName,
		Method:    http.MethodPost,
		FormFields: map[string]string{
			"access_token":     token,
			"access_token_ttl": fmt.Sprintf("%d", expiresAt.UnixMilli()),
		},
		WOPISrc:   s.wopiSrc(file.ID),
		ExpiresAt: expiresAt,
	}, nil
}

func (s *WOPIService) AuthenticateToken(rawToken, fileID string) (*WOPITokenClaims, error) {
	if !s.Enabled() {
		return nil, ErrWOPIDisabled
	}
	var env wopiTokenEnvelope
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(rawToken))
	if err != nil {
		return nil, ErrWOPIUnauthorized
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, ErrWOPIUnauthorized
	}
	mac := hmac.New(sha256.New, []byte(s.filesSecret()))
	mac.Write([]byte(env.Payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(env.Sig)) {
		return nil, ErrWOPIUnauthorized
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, ErrWOPIUnauthorized
	}
	var claims WOPITokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, ErrWOPIUnauthorized
	}
	if claims.FileID != strings.TrimSpace(fileID) || claims.ExpiresAt < time.Now().Unix() {
		return nil, ErrWOPIUnauthorized
	}
	return &claims, nil
}

func (s *WOPIService) CheckFileInfo(ctx context.Context, fileID string, claims *WOPITokenClaims) (*WOPICheckFileInfo, error) {
	file, err := s.files.GetFile(fileID)
	if err != nil {
		return nil, err
	}
	size := int64(0)
	if file.Object != nil {
		size = file.Object.Size
	}
	return &WOPICheckFileInfo{
		BaseFileName:     file.Name,
		Size:             size,
		Version:          fileVersion(file),
		UserID:           claims.AccountID,
		UserFriendlyName: claims.AccountID,
		UserCanWrite:     claims.Permission == "edit",
		ReadOnly:         claims.Permission != "edit",
		SupportsLocks:    true,
		SupportsGetLock:  true,
		SupportsUpdate:   claims.Permission == "edit",
		SupportsRename:   false,
		UserCanRename:    false,
	}, nil
}

func (s *WOPIService) OpenContents(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	file, err := s.files.GetFile(fileID)
	if err != nil {
		return nil, "", err
	}
	key := strings.TrimSpace(stringPtr(file.StorageKey))
	if key == "" && file.Object != nil && file.Object.StorageKey != nil {
		key = strings.TrimSpace(*file.Object.StorageKey)
	}
	if key == "" && file.ObjectID != nil {
		key = strings.TrimSpace(*file.ObjectID)
	}
	if key == "" {
		return nil, "", fmt.Errorf("file storage key missing")
	}
	backend, err := s.files.BackendForFile(file)
	if err != nil {
		return nil, "", err
	}
	reader, _, err := backend.Get(ctx, key)
	if err != nil {
		return nil, "", err
	}
	contentType := file.ResponseMimeType()
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return reader, contentType, nil
}

func (s *WOPIService) SaveContents(ctx context.Context, fileID string, claims *WOPITokenClaims, lockID string, body io.Reader, contentType string) (*database.CloudFile, error) {
	if claims == nil || claims.Permission != "edit" {
		return nil, ErrWOPIUnauthorized
	}
	if currentLock, err := s.currentLock(ctx, fileID); err != nil {
		return nil, err
	} else if currentLock != "" && currentLock != strings.TrimSpace(lockID) {
		return nil, ErrWOPIConflict
	}
	tempDir := os.TempDir()
	tempPath := filepath.Join(tempDir, database.NewID()+".wopi")
	out, err := os.Create(tempPath)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(out, body); err != nil {
		_ = out.Close()
		_ = os.Remove(tempPath)
		return nil, err
	}
	_ = out.Close()
	defer os.Remove(tempPath)

	analysis, _ := s.files.AnalyzeSourceFile(ctx, tempPath, contentType)
	if updated, applied, err := s.files.FastOverwriteFile(fileID, tempPath, analysis); err != nil {
		return nil, err
	} else if applied {
		return updated, nil
	}

	object, err := s.files.DetectAndCreateObject(tempPath)
	if err != nil {
		return nil, err
	}
	storageKey := &object.ID
	updated, err := s.files.OverwriteFile(fileID, object.ID, storageKey)
	if err != nil {
		return nil, err
	}
	if analysis != nil {
		if analyzed, err := s.files.StoreSourceAnalysis(updated.ID, analysis); err == nil {
			updated = analyzed
		}
	}
	stage, err := os.Open(tempPath)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	target := object.ID
	if updated.ObjectID != nil && strings.TrimSpace(*updated.ObjectID) != "" {
		target = strings.TrimSpace(*updated.ObjectID)
	}
	detectedType := contentType
	if strings.TrimSpace(detectedType) == "" {
		if detected, err := mimetype.DetectFile(tempPath); err == nil {
			detectedType = detected.String()
		}
	}
	if err := s.files.Storage().Put(ctx, target, stage, detectedType); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *WOPIService) HandleLock(ctx context.Context, fileID string, claims *WOPITokenClaims, operation, lockID, oldLockID string) (*WOPILockResult, error) {
	if claims == nil || claims.Permission != "edit" {
		return nil, ErrWOPIUnauthorized
	}
	operation = strings.ToUpper(strings.TrimSpace(operation))
	lockID = strings.TrimSpace(lockID)
	oldLockID = strings.TrimSpace(oldLockID)
	if operation == "" {
		return nil, ErrWOPIInvalidLockOperation
	}
	return s.withLockTx(ctx, fileID, claims, operation, lockID, oldLockID)
}

func (s *WOPIService) withLockTx(ctx context.Context, fileID string, claims *WOPITokenClaims, operation, lockID, oldLockID string) (*WOPILockResult, error) {
	result := &WOPILockResult{}
	err := s.files.DB().Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("expires_at <= ?", time.Now()).Delete(&database.WOPILock{}).Error
		var current database.WOPILock
		err := tx.Where("file_id = ?", fileID).First(&current).Error
		hasCurrent := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if hasCurrent {
			result.CurrentLock = current.LockID
		}
		accountID := parseOptionalUUID(claims.AccountID)
		switch operation {
		case "GET_LOCK":
			return nil
		case "LOCK":
			if lockID == "" {
				return ErrWOPIInvalidLockOperation
			}
			if hasCurrent && current.LockID != lockID {
				return ErrWOPIConflict
			}
			if hasCurrent {
				return tx.Model(&database.WOPILock{}).Where("id = ?", current.ID).Updates(map[string]any{
					"lock_id":    lockID,
					"account_id": accountID,
					"expires_at": time.Now().Add(30 * time.Minute),
				}).Error
			}
			return tx.Create(&database.WOPILock{
				FileID:    fileID,
				LockID:    lockID,
				AccountID: accountID,
				ExpiresAt: time.Now().Add(30 * time.Minute),
			}).Error
		case "REFRESH_LOCK":
			if !hasCurrent || current.LockID != lockID {
				return ErrWOPIConflict
			}
			return tx.Model(&database.WOPILock{}).Where("id = ?", current.ID).Update("expires_at", time.Now().Add(30*time.Minute)).Error
		case "UNLOCK":
			if !hasCurrent || current.LockID != lockID {
				return ErrWOPIConflict
			}
			return tx.Delete(&database.WOPILock{}, "id = ?", current.ID).Error
		case "UNLOCK_AND_RELOCK":
			expected := oldLockID
			if expected == "" {
				expected = lockID
			}
			if !hasCurrent || current.LockID != expected || lockID == "" {
				return ErrWOPIConflict
			}
			result.CurrentLock = lockID
			return tx.Model(&database.WOPILock{}).Where("id = ?", current.ID).Updates(map[string]any{
				"lock_id":    lockID,
				"account_id": accountID,
				"expires_at": time.Now().Add(30 * time.Minute),
			}).Error
		default:
			return ErrWOPIInvalidLockOperation
		}
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *WOPIService) currentLock(ctx context.Context, fileID string) (string, error) {
	_ = s.files.DB().WithContext(ctx).Where("expires_at <= ?", time.Now()).Delete(&database.WOPILock{}).Error
	var current database.WOPILock
	if err := s.files.DB().WithContext(ctx).Where("file_id = ?", fileID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return current.LockID, nil
}

func (s *WOPIService) signToken(claims WOPITokenClaims) (string, time.Time, error) {
	expiresAt := time.Now().Add(s.cfg.TokenTTL)
	claims.ExpiresAt = expiresAt.Unix()
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, []byte(s.filesSecret()))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	tokenJSON, err := json.Marshal(wopiTokenEnvelope{Payload: payload, Sig: sig})
	if err != nil {
		return "", time.Time{}, err
	}
	return base64.RawURLEncoding.EncodeToString(tokenJSON), expiresAt, nil
}

func (s *WOPIService) actionURL(ctx context.Context, fileName, wopiSrc, actionName string) (string, error) {
	discovery, err := s.loadDiscovery(ctx)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	actions := discovery.Actions[ext]
	if len(actions) == 0 {
		return "", ErrWOPIUnsupportedFile
	}
	urlSrc := strings.TrimSpace(actions[actionName])
	if urlSrc == "" {
		return "", ErrWOPIUnsupportedFile
	}
	parsed, err := url.Parse(urlSrc)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("WOPISrc", wopiSrc)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *WOPIService) loadDiscovery(ctx context.Context) (*wopiDiscoveryData, error) {
	s.mu.Lock()
	if s.discovery != nil && time.Now().Before(s.discoveryUntil) {
		data := s.discovery
		s.mu.Unlock()
		return data, nil
	}
	s.mu.Unlock()

	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.CollaboraURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("wopi collaboraUrl is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/hosting/discovery", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("collabora discovery returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var doc wopiDiscoveryXML
	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&doc); err != nil {
		return nil, err
	}
	data := &wopiDiscoveryData{Actions: map[string]map[string]string{}}
	for _, zone := range doc.NetZones {
		for _, app := range zone.Apps {
			for _, action := range app.Actions {
				ext := strings.ToLower(strings.TrimSpace(action.Ext))
				name := strings.ToLower(strings.TrimSpace(action.Name))
				if ext == "" || name == "" || strings.TrimSpace(action.URLSrc) == "" {
					continue
				}
				if data.Actions[ext] == nil {
					data.Actions[ext] = map[string]string{}
				}
				if _, ok := data.Actions[ext][name]; !ok {
					data.Actions[ext][name] = action.URLSrc
				}
			}
		}
	}
	s.mu.Lock()
	s.discovery = data
	s.discoveryUntil = time.Now().Add(5 * time.Minute)
	s.mu.Unlock()
	return data, nil
}

func (s *WOPIService) wopiSrc(fileID string) string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.PublicURL), "/")
	return base + "/wopi/files/" + url.PathEscape(strings.TrimSpace(fileID))
}

func (s *WOPIService) filesSecret() string {
	return s.files.AccessSecret()
}

func fileVersion(file *database.CloudFile) string {
	if file == nil {
		return ""
	}
	if file.Object != nil && strings.TrimSpace(file.Object.Hash) != "" {
		return strings.TrimSpace(file.Object.Hash)
	}
	return fmt.Sprintf("%d", file.UpdatedAt.UnixMilli())
}

func parseOptionalUUID(value string) *uuid.UUID {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func stringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
