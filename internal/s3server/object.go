package s3server

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func (s *Server) handleGetObject(w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()
	reader, info, err := s.backend.Get(ctx, key)
	if err != nil {
		xmlError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", "/"+key)
		return
	}
	defer func() { _ = reader.Close() }()

	if info.MimeType != "" {
		w.Header().Set("Content-Type", info.MimeType)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	if info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	if !info.ModTime.IsZero() {
		w.Header().Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("Accept-Ranges", "bytes")

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		s.handleRangeRequest(w, r, reader, info, rangeHeader)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func (s *Server) handleRangeRequest(w http.ResponseWriter, r *http.Request, reader io.ReadCloser, info storage.ObjectInfo, rangeHeader string) {
	start, end, ok := parseRange(rangeHeader, info.Size)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	length := end - start + 1
	if seeker, ok := reader.(io.Seeker); ok {
		_, _ = seeker.Seek(start, io.SeekStart)
	}

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)

	_, _ = io.CopyN(w, reader, length)
}

func parseRange(header string, size int64) (start, end int64, ok bool) {
	if !strings.HasPrefix(header, "bytes=") {
		return
	}
	header = header[6:]
	parts := strings.SplitN(header, "-", 2)
	if len(parts) != 2 {
		return
	}
	if parts[0] == "" {
		length, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || length <= 0 {
			return
		}
		start = size - length
		if start < 0 {
			start = 0
		}
		end = size - 1
		ok = true
		return
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return
	}
	if parts[1] == "" {
		end = size - 1
	} else {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start || end >= size {
			end = size - 1
		}
	}
	ok = true
	return
}

func (s *Server) handlePutObject(w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if err := s.backend.Put(ctx, key, r.Body, contentType); err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+key)
		return
	}

	info, err := s.backend.Stat(ctx, key)
	if err == nil && info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleHeadObject(w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()
	info, err := s.backend.Stat(ctx, key)
	if err != nil {
		xmlError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", "/"+key)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	if info.MimeType != "" {
		w.Header().Set("Content-Type", info.MimeType)
	}
	if info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	if !info.ModTime.IsZero() {
		w.Header().Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()
	if err := s.backend.Delete(ctx, key); err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+key)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInitiateMultipartUpload(w http.ResponseWriter, r *http.Request, key string) {
	uploadID := fmt.Sprintf("%d", time.Now().UnixNano())
	s.mu.Lock()
	s.multipart[uploadID] = &multipartUpload{key: key, parts: make(map[int][]byte)}
	s.mu.Unlock()

	result := InitiateMultipartUploadResult{
		Bucket:   "default",
		Key:      key,
		UploadID: uploadID,
	}
	xmlResponse(w, http.StatusOK, result)
}

func (s *Server) handleUploadPart(w http.ResponseWriter, r *http.Request, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	partNumberStr := r.URL.Query().Get("partNumber")

	if uploadID == "" || partNumberStr == "" {
		xmlError(w, http.StatusBadRequest, "InvalidArgument", "uploadId and partNumber are required", "/"+key)
		return
	}

	partNumber, err := strconv.Atoi(partNumberStr)
	if err != nil || partNumber < 1 {
		xmlError(w, http.StatusBadRequest, "InvalidArgument", "invalid partNumber", "/"+key)
		return
	}

	s.mu.Lock()
	upload, ok := s.multipart[uploadID]
	s.mu.Unlock()
	if !ok {
		xmlError(w, http.StatusNotFound, "NoSuchUpload", "The specified upload does not exist.", "/"+key)
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+key)
		return
	}

	hash := md5.Sum(data)
	etag := "\"" + hex.EncodeToString(hash[:]) + "\""

	s.mu.Lock()
	upload.parts[partNumber] = data
	s.mu.Unlock()

	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCompleteMultipartUpload(w http.ResponseWriter, r *http.Request, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		xmlError(w, http.StatusBadRequest, "InvalidArgument", "uploadId is required", "/"+key)
		return
	}

	s.mu.Lock()
	upload, ok := s.multipart[uploadID]
	if !ok {
		s.mu.Unlock()
		xmlError(w, http.StatusNotFound, "NoSuchUpload", "The specified upload does not exist.", "/"+key)
		return
	}
	delete(s.multipart, uploadID)
	s.mu.Unlock()

	if err := upload.complete(r.Context(), s.backend); err != nil {
		xmlError(w, http.StatusInternalServerError, "InternalError", err.Error(), "/"+key)
		return
	}

	allHash := md5.Sum([]byte(uploadID))
	etag := "\"" + hex.EncodeToString(allHash[:]) + "\""

	result := CompleteMultipartUploadResult{
		Location: "http://" + r.Host + "/" + key,
		Bucket:   "default",
		Key:      key,
		ETag:     etag,
	}
	xmlResponse(w, http.StatusOK, result)
}

func (s *Server) handleAbortMultipartUpload(w http.ResponseWriter, r *http.Request, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		xmlError(w, http.StatusBadRequest, "InvalidArgument", "uploadId is required", "/"+key)
		return
	}
	s.mu.Lock()
	delete(s.multipart, uploadID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
