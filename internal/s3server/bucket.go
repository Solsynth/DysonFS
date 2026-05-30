package s3server

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, err := s.backend.List(ctx, "")
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/")
		return
	}

	buckets := extractBuckets(keys)
	result := ListAllMyBucketsResult{
		Owner: BucketOwner{ID: "dysonfs", DisplayName: "DysonFS"},
		Buckets: buckets,
	}
	xmlResponse(w, http.StatusOK, result)
}

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	prefix := r.URL.Query().Get("prefix")
	delimiter := r.URL.Query().Get("delimiter")
	maxKeys := 1000
	if v := r.URL.Query().Get("max-keys"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 {
			maxKeys = n
		}
	}
	marker := r.URL.Query().Get("marker")
	if marker == "" {
		marker = r.URL.Query().Get("start-after")
	}

	keys, err := s.backend.List(ctx, prefix)
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}

	sort.Strings(keys)

	if marker != "" {
		filtered := make([]string, 0, len(keys))
		for _, k := range keys {
			if k > marker {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	entries := make([]ObjectEntry, 0, len(keys))
	commonPrefixes := make(map[string]struct{})

	for _, key := range keys {
		if delimiter != "" {
			relative := strings.TrimPrefix(key, prefix)
			if idx := strings.Index(relative, delimiter); idx >= 0 {
				cp := prefix + relative[:idx+len(delimiter)]
				commonPrefixes[cp] = struct{}{}
				continue
			}
		}

		if len(entries) >= maxKeys {
			break
		}

		info, err := s.backend.Stat(ctx, key)
		if err != nil {
			continue
		}

		etag := info.ETag
		if etag == "" {
			etag = "\"" + key + "\""
		}

		entries = append(entries, ObjectEntry{
			Key:          key,
			LastModified: info.ModTime.UTC().Format("2006-01-02T15:04:05.000Z"),
			Size:         info.Size,
			ETag:         etag,
			StorageClass: "STANDARD",
		})
	}

	result := ListBucketResult{
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(entries),
		MaxKeys:     maxKeys,
		IsTruncated: false,
		Contents:    entries,
	}
	xmlResponse(w, http.StatusOK, result)
}

func (s *Server) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	keys, err := s.backend.List(ctx, "")
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}
	for _, key := range keys {
		if strings.HasPrefix(key, bucket+"/") || key == bucket {
			xmlError(w, http.StatusConflict, "BucketNotEmpty", "bucket is not empty", "/"+bucket)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func extractBuckets(keys []string) []BucketEntry {
	seen := make(map[string]struct{})
	var buckets []BucketEntry
	for _, key := range keys {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		buckets = append(buckets, BucketEntry{
			Name:         name,
			CreationDate: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}
	if len(buckets) == 0 {
		buckets = []BucketEntry{{Name: "default", CreationDate: time.Now().UTC().Format("2006-01-02T15:04:05.000Z")}}
	}
	return buckets
}

func (s *Server) handleListObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	prefix := r.URL.Query().Get("prefix")
	delimiter := r.URL.Query().Get("delimiter")
	maxKeys := 1000
	if v := r.URL.Query().Get("max-keys"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 {
			maxKeys = n
		}
	}
	continuationToken := r.URL.Query().Get("continuation-token")
	startAfter := r.URL.Query().Get("start-after")

	keys, err := s.backend.List(ctx, prefix)
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}
	sort.Strings(keys)

	marker := startAfter
	if continuationToken != "" {
		marker = continuationToken
	}
	if marker != "" {
		filtered := make([]string, 0, len(keys))
		for _, k := range keys {
			if k > marker {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	entries := make([]ObjectEntry, 0)
	for _, key := range keys {
		if delimiter != "" {
			relative := strings.TrimPrefix(key, prefix)
			if idx := strings.Index(relative, delimiter); idx >= 0 {
				continue
			}
		}
		if len(entries) >= maxKeys {
			break
		}
		info, err := s.backend.Stat(ctx, key)
		if err != nil {
			continue
		}
		etag := info.ETag
		if etag == "" {
			etag = "\"" + key + "\""
		}
		entries = append(entries, ObjectEntry{
			Key:          key,
			LastModified: info.ModTime.UTC().Format("2006-01-02T15:04:05.000Z"),
			Size:         info.Size,
			ETag:         etag,
			StorageClass: "STANDARD",
		})
	}

	result := ListBucketResult{
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(entries),
		MaxKeys:     maxKeys,
		IsTruncated: false,
		Contents:    entries,
	}
	xmlResponse(w, http.StatusOK, result)
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalidArgument
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var ErrInvalidArgument = &s3Error{Code: "InvalidArgument", Message: "invalid argument"}

type s3Error struct {
	Code    string
	Message string
}

func (e *s3Error) Error() string { return e.Message }
