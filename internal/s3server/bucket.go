package s3server

import (
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	buckets, err := s.backend.ListBuckets(ctx)
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/")
		return
	}
	entries := make([]BucketEntry, len(buckets))
	for i, b := range buckets {
		entries[i] = BucketEntry{
			Name:         b.Name,
			CreationDate: b.CreationDate.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
	}
	result := ListAllMyBucketsResult{
		Owner:   BucketOwner{ID: "dysonfs", DisplayName: "DysonFS"},
		Buckets: entries,
	}
	xmlResponse(w, http.StatusOK, result)
}

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	prefix := r.URL.Query().Get("prefix")
	marker := r.URL.Query().Get("marker")
	if marker == "" {
		marker = r.URL.Query().Get("start-after")
	}
	maxKeys := 1000
	if v := r.URL.Query().Get("max-keys"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxKeys = n
		}
	}

	entries, isTruncated, err := s.backend.ListObjects(ctx, bucket, prefix, marker, maxKeys)
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}

	result := ListBucketResult{
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(entries),
		MaxKeys:     maxKeys,
		IsTruncated: isTruncated,
		Contents:    entries,
	}
	xmlResponse(w, http.StatusOK, result)
}

func (s *Server) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	if err := s.backend.HeadBucket(ctx, bucket); err != nil {
		xmlError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", "/"+bucket)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	if err := s.backend.CreateBucket(ctx, bucket); err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	if err := s.backend.DeleteBucket(ctx, bucket); err != nil {
		if strings.Contains(err.Error(), "not empty") {
			xmlError(w, http.StatusConflict, "BucketNotEmpty", "bucket is not empty", "/"+bucket)
			return
		}
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	prefix := r.URL.Query().Get("prefix")
	startAfter := r.URL.Query().Get("start-after")
	continuationToken := r.URL.Query().Get("continuation-token")
	maxKeys := 1000
	if v := r.URL.Query().Get("max-keys"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxKeys = n
		}
	}

	marker := startAfter
	if continuationToken != "" {
		marker = continuationToken
	}

	entries, isTruncated, err := s.backend.ListObjects(ctx, bucket, prefix, marker, maxKeys)
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+bucket)
		return
	}

	result := ListBucketResult{
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(entries),
		MaxKeys:     maxKeys,
		IsTruncated: isTruncated,
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
