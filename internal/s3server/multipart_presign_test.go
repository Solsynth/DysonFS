package s3server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// newNoAuthS3Server starts the mock without credential checks so presigned
// (query-auth) URLs are accepted verbatim.
func newNoAuthS3Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New(newMockBackend(), "", "")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func putRaw(t *testing.T, url string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new PUT request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s: status = %d", url, resp.StatusCode)
	}
}

// TestS3BackendMultipartPresignedUpload exercises storage.S3Backend's
// multipart direct-upload methods end to end against the mock S3 server:
// initiate, presign each part, PUT parts, list, complete, stat, and the abort
// path.
func TestS3BackendMultipartPresignedUpload(t *testing.T) {
	ts := newNoAuthS3Server(t)
	addr := ts.Listener.Addr().String()
	backend, err := storage.NewS3Backend(addr, "test-access-key", "test-secret-key", "testbucket", false)
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}
	var _ storage.MultipartDirectUploadBackend = backend

	ctx := context.Background()
	if err := makeBucket(addr, "testbucket"); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	key := "uploads/task-1/source"
	uploadID, err := backend.CreateMultipartUpload(ctx, key, "application/octet-stream")
	if err != nil {
		t.Fatalf("CreateMultipartUpload() error = %v", err)
	}
	if uploadID == "" {
		t.Fatal("CreateMultipartUpload() returned empty upload id")
	}

	// Upload three parts through presigned URLs.
	total := 0
	for i := 1; i <= 3; i++ {
		url, err := backend.PresignPartUpload(ctx, key, uploadID, i, 5*time.Minute)
		if err != nil {
			t.Fatalf("PresignPartUpload(%d) error = %v", i, err)
		}
		wantParam := fmt.Sprintf("partNumber=%d", i)
		if !strings.Contains(url, wantParam) {
			t.Fatalf("presigned URL %s missing %s", url, wantParam)
		}
		if !strings.Contains(url, "uploadId=") {
			t.Fatalf("presigned URL %s missing uploadId", url)
		}
		part := bytes.Repeat([]byte{byte(i)}, i*1024)
		putRaw(t, url, part)
		total += len(part)
	}

	parts, err := backend.ListParts(ctx, key, uploadID)
	if err != nil {
		t.Fatalf("ListParts() error = %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("ListParts() got %d parts, want 3: %+v", len(parts), parts)
	}
	var listedSize int64
	for idx, part := range parts {
		if part.PartNumber != idx+1 {
			t.Fatalf("part %d has part number %d, want %d", idx, part.PartNumber, idx+1)
		}
		if part.ETag == "" {
			t.Fatalf("part %d has empty etag", part.PartNumber)
		}
		listedSize += part.Size
	}
	if listedSize != int64(total) {
		t.Fatalf("listed part sizes sum = %d, want %d", listedSize, total)
	}

	if err := backend.CompleteMultipartUpload(ctx, key, uploadID, parts); err != nil {
		t.Fatalf("CompleteMultipartUpload() error = %v", err)
	}
	info, err := backend.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat() after complete error = %v", err)
	}
	if info.Size != int64(total) {
		t.Fatalf("Stat() size = %d, want %d", info.Size, total)
	}

	// Abort path: a partially uploaded session must be discarded and the
	// object must not exist afterwards.
	abortKey := key + "-abort"
	abortID, err := backend.CreateMultipartUpload(ctx, abortKey, "")
	if err != nil {
		t.Fatalf("CreateMultipartUpload(abort) error = %v", err)
	}
	url, err := backend.PresignPartUpload(ctx, abortKey, abortID, 1, time.Minute)
	if err != nil {
		t.Fatalf("PresignPartUpload(abort) error = %v", err)
	}
	putRaw(t, url, []byte("partial"))
	if err := backend.AbortMultipartUpload(ctx, abortKey, abortID); err != nil {
		t.Fatalf("AbortMultipartUpload() error = %v", err)
	}
	if _, err := backend.ListParts(ctx, abortKey, abortID); err == nil {
		t.Fatal("ListParts() after abort succeeded, want NoSuchUpload error")
	}
	if _, err := backend.Stat(ctx, abortKey); err == nil {
		t.Fatal("Stat() after abort succeeded, object must not exist")
	}
}

func makeBucket(addr, bucket string) error {
	req, err := http.NewRequest(http.MethodPut, "http://"+addr+"/"+bucket, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("create bucket status = %d", resp.StatusCode)
	}
	return nil
}
