# Content Edit API

DysonFS provides a binary diff/patch API for efficiently editing file content without re-uploading the entire file. Combined with file locks, this enables safe concurrent editing workflows.

## Binary Patch Endpoint

Apply a binary diff (bsdiff format) to an existing file.

### Request

```http
PATCH /api/files/:id/content HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/x-binary-patch
Content-Length: <patch-size>

<binary-diff-data>
```

### Parameters

| Parameter | Type | Location | Description |
|-----------|------|----------|-------------|
| `id` | string | path | File ID to patch |
| `X-Lock-Token` | string | header | (Optional) Lock token to verify ownership |

### Lock Requirement

The file **must** have an active lock before patching. This prevents conflicting edits from different clients. The lock can be acquired via:

- **WebDAV**: `LOCK` method
- **WOPI**: `X-WOPI-Override: LOCK` header
- **API**: Use the lock acquisition mechanism in your workflow

If no lock exists, the server returns `423 Locked`.

### Responses

| Status | Description |
|--------|-------------|
| `200 OK` | Patch applied successfully. Returns updated file JSON. |
| `401 Unauthorized` | Missing or invalid auth token. |
| `403 Forbidden` | No write permission on the file. |
| `404 Not Found` | File does not exist. |
| `409 Conflict` | Patch data is corrupt or cannot be applied to the current file content. |
| `423 Locked` | File is not locked, or lock token does not match. |
| `500 Internal Server Error` | Storage or database error. |

### Response Body

```json
{
  "id": "01HZX...",
  "name": "report.pdf",
  "size": 1048576,
  "mime_type": "application/pdf",
  "hash": "a1b2c3...",
  "object_id": "01HZX...",
  "updated_at": "2026-05-30T12:00:00Z"
}
```

## Generating Binary Diffs

The patch format is **bsdiff** (as produced by `github.com/kr/binarydist`). You can generate patches on the client side using any bsdiff-compatible tool.

### Go (client-side)

```go
import "github.com/kr/binarydist"

// Generate a diff between old and new content
var patch bytes.Buffer
err := binarydist.Diff(
    bytes.NewReader(oldContent),
    bytes.NewReader(newContent),
    &patch,
)
// Send patch.Bytes() to PATCH /api/files/:id/content
```

### Python

```python
import bsdiff4

patch = bsdiff4.diff(old_content, new_content)
# Send patch bytes to PATCH /api/files/:id/content
```

### CLI (bsdiff/bspatch)

```bash
# Generate patch
bsdiff old_file new_file patch_file

# Send patch
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/x-binary-patch" \
  --data-binary @patch_file \
  http://localhost:8080/api/files/$FILE_ID/content
```

## Full Overwrite (Direct Upload)

For cases where a full file replacement is more efficient than a diff (e.g., completely new content), use the direct upload endpoint.

### Request

```http
POST /api/files/upload/direct HTTP/1.1
Authorization: Bearer <token>
Content-Type: multipart/form-data; boundary=---

-----
Content-Disposition: form-data; name="file"; filename="report.pdf"
Content-Type: application/pdf

<file-content>
-----
Content-Disposition: form-data; name="overwrite_id"

01HZX...
-----
Content-Disposition: form-data; name="fast_mode"

true
--------
```

### Form Fields

| Field | Type | Description |
|-------|------|-------------|
| `file` | file | The file content to upload |
| `overwrite_id` | string | (Optional) File ID to overwrite |
| `fast_mode` | bool | (Optional) Use fast overwrite when possible (reuses storage key) |
| `parent_id` | string | (Optional) Parent folder ID |
| `description` | string | (Optional) File description |
| `hash` | string | (Optional) Expected SHA-256 hash for verification |
| `usage` | string | (Optional) File usage tag |
| `application_type` | string | (Optional) Application type |
| `expired_at` | string | (Optional) Expiration time (RFC3339) |
| `index` | bool | (Optional) Whether to index the file |

### Fast Mode

When `fast_mode=true` and `overwrite_id` is set, the server checks if the file's storage object is exclusively owned (referenced by only one file). If so, it overwrites the storage in-place without creating a new object. This is faster for large files with small changes, but still requires uploading the full file content.

## Chunked Upload

For large files, use the chunked upload flow.

### Step 1: Create Upload Task

```http
POST /api/files/upload/create HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json

{
  "file_name": "video.mp4",
  "file_size": 1073741824,
  "chunk_size": 5242880,
  "overwrite_id": "01HZX...",
  "fast_mode": true
}
```

Response:
```json
{
  "task_id": "01HZX...",
  "chunk_size": 5242880,
  "chunks_count": 205
}
```

### Step 2: Upload Chunks

```http
POST /api/files/upload/chunk/:taskId/:idx HTTP/1.1
Authorization: Bearer <token>
Content-Type: multipart/form-data

<chunk-data>
```

Upload each chunk (0-indexed). Chunks can be uploaded in parallel.

### Step 3: Complete Upload

```http
POST /api/files/upload/complete/:taskId HTTP/1.1
Authorization: Bearer <token>
```

The server merges chunks, detects content type, and creates/overwrites the file.

### Resume Support

Check which chunks have been uploaded:

```http
GET /api/files/upload/resume/:taskId HTTP/1.1
Authorization: Bearer <token>
```

Response includes `chunks_uploaded` array listing indices of already-uploaded chunks.

## Workflow: Edit-in-Place

A typical client workflow for editing a file in place:

```
1. Acquire lock (WebDAV LOCK or WOPI edit session)
2. Read current file content:     GET /api/files/:id/open
3. Make local edits
4. Generate binary diff:          bsdiff(old, new) → patch
5. Send patch:                    PATCH /api/files/:id/content
6. Release lock (WebDAV UNLOCK)
```

For very large files where generating a diff is expensive, use the full overwrite path:

```
1. Acquire lock
2. Upload full content:           POST /api/files/upload/direct (with overwrite_id)
3. Release lock
```

## Write Pipeline (Internal)

All write paths use a streaming pipeline that avoids loading entire files into memory:

```
io.Reader → temp file (disk) → SHA-256 hash + MIME detect → storage backend → DB record
```

- Hash is computed via `io.TeeReader` as data flows to the temp file (zero extra memory)
- MIME type is detected from the first 512 bytes
- Storage upload streams from the temp file (no full-file memory load)
- Works with both local and S3 storage backends
