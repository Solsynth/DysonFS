package s3server

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
)

type multipartUpload struct {
	bucket string
	key    string
	parts  map[int][]byte
}

func (u *multipartUpload) complete(ctx context.Context, backend Backend) error {
	var combined []byte
	for i := 1; i <= len(u.parts); i++ {
		data, ok := u.parts[i]
		if !ok {
			continue
		}
		combined = append(combined, data...)
	}
	return backend.PutObject(ctx, u.bucket, u.key, bytes.NewReader(combined), int64(len(combined)), "application/octet-stream")
}

type Server struct {
	backend   Backend
	accessKey string
	secretKey string
	resolver  TokenResolver
	mu        sync.Mutex
	multipart map[string]*multipartUpload
}

func New(backend Backend, accessKey, secretKey string) *Server {
	return &Server{
		backend:   backend,
		accessKey: accessKey,
		secretKey: secretKey,
		multipart: make(map[string]*multipartUpload),
	}
}

func NewWithResolver(backend Backend, resolver TokenResolver) *Server {
	return &Server{
		backend:   backend,
		resolver:  resolver,
		multipart: make(map[string]*multipartUpload),
	}
}

func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.resolver != nil || (s.accessKey != "" && s.secretKey != "") {
			result, ok := authenticateRequest(r, s.accessKey, s.secretKey, s.resolver)
			if !ok {
				xmlError(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match.", r.URL.Path)
				return
			}
			if result.info != nil {
				ctx := context.WithValue(r.Context(), tokenInfoContextKey, result.info)
				r = r.WithContext(ctx)
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
			s.handleInitiateMultipartUpload(w, r, bucket, key)
			return
		}
		if r.URL.Query().Get("uploadId") != "" {
			switch r.Method {
			case http.MethodPost:
				s.handleCompleteMultipartUpload(w, r, bucket, key)
			case http.MethodPut:
				s.handleUploadPart(w, r, bucket, key)
			case http.MethodDelete:
				s.handleAbortMultipartUpload(w, r, bucket, key)
			default:
				xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.", "/"+key)
			}
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.handleGetObject(w, r, bucket, key)
		case http.MethodHead:
			s.handleHeadObject(w, r, bucket, key)
		case http.MethodPut:
			s.handlePutObject(w, r, bucket, key)
		case http.MethodDelete:
			s.handleDeleteObject(w, r, bucket, key)
		default:
			xmlError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.", "/"+key)
		}
	}
}
