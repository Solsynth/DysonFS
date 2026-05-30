package s3server

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"

	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

type multipartUpload struct {
	key   string
	parts map[int][]byte
}

func (u *multipartUpload) complete(ctx context.Context, backend storage.Backend) error {
	var combined []byte
	for i := 1; i <= len(u.parts); i++ {
		data, ok := u.parts[i]
		if !ok {
			continue
		}
		combined = append(combined, data...)
	}
	return backend.Put(ctx, u.key, bytes.NewReader(combined), "application/octet-stream")
}

type Server struct {
	backend   storage.Backend
	accessKey string
	secretKey string
	mu        sync.Mutex
	multipart map[string]*multipartUpload
}

func New(backend storage.Backend, accessKey, secretKey string) *Server {
	return &Server{
		backend:   backend,
		accessKey: accessKey,
		secretKey: secretKey,
		multipart: make(map[string]*multipartUpload),
	}
}

func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.accessKey != "" && s.secretKey != "" {
			if !validateSignature(r, s.accessKey, s.secretKey) {
				xmlError(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match.", r.URL.Path)
				return
			}
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			s.handleListBuckets(w, r)
			return
		}

		parts := strings.SplitN(path, "/", 2)
		bucket := parts[0]
		key := ""
		if len(parts) > 1 {
			key = parts[1]
		}

		if key == "" {
			switch r.Method {
			case http.MethodGet:
				if r.URL.Query().Get("list-type") == "2" {
					s.handleListObjectsV2(w, r, bucket)
				} else {
					s.handleListObjects(w, r, bucket)
				}
			case http.MethodHead:
				s.handleHeadBucket(w, r, bucket)
			case http.MethodPut:
				s.handleCreateBucket(w, r, bucket)
			case http.MethodDelete:
				s.handleDeleteBucket(w, r, bucket)
			default:
				xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.", "/"+bucket)
			}
			return
		}

		if r.URL.Query().Get("uploads") != "" && r.Method == http.MethodPost {
			s.handleInitiateMultipartUpload(w, r, key)
			return
		}
		if r.URL.Query().Get("uploadId") != "" {
			switch r.Method {
			case http.MethodPost:
				s.handleCompleteMultipartUpload(w, r, key)
			case http.MethodPut:
				s.handleUploadPart(w, r, key)
			case http.MethodDelete:
				s.handleAbortMultipartUpload(w, r, key)
			default:
				xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.", "/"+key)
			}
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.handleGetObject(w, r, key)
		case http.MethodHead:
			s.handleHeadObject(w, r, key)
		case http.MethodPut:
			s.handlePutObject(w, r, key)
		case http.MethodDelete:
			s.handleDeleteObject(w, r, key)
		default:
			xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.", "/"+key)
		}
	}
}
