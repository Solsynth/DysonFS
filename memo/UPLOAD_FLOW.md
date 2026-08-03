# Upload Flow

DysonFS supports two upload paths:

1. **Proxied upload** — the client sends file bytes to DysonFS using the
   existing direct or chunked upload endpoints.
2. **S3 direct upload** — DysonFS authorizes the upload, returns a short-lived
   presigned `PUT` URL, and the client sends the file bytes directly to the
   selected S3-compatible pool.

The direct path avoids routing large file bodies through the DysonFS HTTP
server. DysonFS still owns authorization, quota checks, object verification,
file creation, and asynchronous processing.

## Statuses

Upload status is an integer exposed as `status` on a `CloudFile` and as
`status` on direct-upload responses:

| Value | Name | Meaning |
|---:|---|---|
| 0 | `Unknown` | Legacy or unset value. New upload tasks do not use this value. |
| 1 | `Uploading` | An upload task exists, but the source object has not been confirmed. |
| 2 | `Processing` | The source object was verified and the visible file is queued for analysis or derivative generation. |
| 3 | `Completed` | Source processing and required derivatives finished successfully. |
| 4 | `Failed` | Processing or upload finalization failed. The task stores an error message. |

The file is created and visible immediately after the direct completion
request succeeds. Its initial status is `Processing`; clients should not wait
for `Completed` before displaying the file.

## Direct Upload Requirements

Direct upload is available only when all of the following are true:

- The selected pool resolves to an S3-compatible backend.
- The pool has `storage_config.enable_signed` enabled.
- The authenticated account has the files-upload permission.
- The account or workspace has enough quota for the declared file size.
- The file complies with the selected pool policy.

Local storage, unsigned S3 pools, and other backends that cannot issue
presigned `PUT` URLs are rejected by the prepare endpoint. The response tells
the client to use a proxied upload instead:

```json
{
  "error": "storage pool does not support direct uploads; use proxied upload",
  "use_proxied_upload": true
}
```

The direct path supports two shapes: a single presigned `PUT` for the whole
object (default), and multipart presigning for large files that want resumable,
parallel part uploads (see [Multipart Direct Upload](#multipart-direct-upload)).

## Flow

### 1. Prepare

Prepare accepts a `hash` (the client's MD5/SHA of the file bytes). When
present, DysonFS first looks for an in-progress direct upload for the same
account, pool, hash, size, and destination (`parent_id`, `workspace_id`,
`overwrite_id`). If one exists, the request resumes it instead of creating a
new task:

- The response returns the existing `task_id`/`upload_id` with `resumed: true`.
- Multipart responses additionally return `uploaded_parts` — the part numbers
  already uploaded for the session — so the client only uploads the missing
  parts.
- The object key stays task-derived (`uploads/<task-id>/source`), so both
  attempts address the same object while the same task is in flight. A
  resumed single-PUT simply overwrites the object in place.
- Resuming refreshes the task's activity, so the hourly expiry sweep keeps
  collecting only genuinely idle sessions.

Keys are always derived from the task id — never from the hash — so an
in-flight upload can never collide with a completed file's object: completed
files keep their `uploads/<task-id>/source` key, and the expiry sweep only
deletes keys of `Uploading` tasks, which no file references. A different
destination (for example the same file uploaded to another folder) never
resumes — it gets its own task and its own key.

The client sends metadata to:

```http
POST /api/files/upload/prepare
Authorization: Bearer <session-token>
Content-Type: application/json
```

Example request:

```json
{
  "file_name": "photo.jpg",
  "file_size": 5242880,
  "content_type": "image/jpeg",
  "hash": "optional-client-sha256",
  "description": "Profile photo",
  "index": true,
  "pool_id": "pool-id",
  "workspace_id": "optional-workspace-id",
  "parent_id": "optional-folder-id",
  "usage": "profile",
  "application_type": "image"
}
```

The request accepts the same main file metadata as the existing upload task
creation endpoint. `overwrite_id` can be supplied to replace an existing file;
the overwrite target controls its name, parent, workspace, description, and
other immutable file metadata.

`file_name` is optional: when omitted (or blank), the server derives a
default name — `upload.<ext>` — from `content_type` (`image/jpeg` →
`upload.jpg`, `application/octet-stream` → `upload.bin`, unknown types →
`upload.bin`), so uploads are never rejected or stored unnamed.

On success, DysonFS creates a persistent task in `Uploading` and returns:

```json
{
  "task_id": "01J...",
  "status": 1,
  "object_key": "uploads/01J.../source",
  "upload_url": "https://s3.example.com/bucket/uploads/01J.../source?...",
  "expires_in": 900,
  "content_type": "image/jpeg"
}
```

The object key is generated by DysonFS. Clients must use the returned URL and
must not replace the key with a user-selected key.

The presigned URL is valid for 15 minutes. The client should upload with the
returned content type:

```http
PUT <upload_url>
Content-Type: image/jpeg

<file bytes>
```

The S3 service response only confirms that the object write completed. It does
not make the DysonFS file visible yet.

### 2. Complete

After the S3 `PUT` succeeds, the client asks DysonFS to verify and commit the
upload:

```http
POST /api/files/upload/<task-id>/complete
Authorization: Bearer <session-token>
```

The endpoint performs these checks:

1. Authenticates the caller and verifies task ownership.
2. Confirms that the task is still `Uploading`.
3. Resolves the task's original storage pool.
4. Calls the backend `Stat` operation against the server-generated object key.
5. Verifies that the remote object size exactly matches the declared file size.
6. Resolves the content type: a real (non-generic) content type stored on the
   object wins, otherwise the type declared in prepare. Pools report
   `application/octet-stream` when the presigned `PUT` carried no
   `Content-Type`, so the generic default never overrides the declared type.
7. Changes the task to `Processing`.
8. Creates the source `FileObject` and visible `CloudFile`.
9. Stores the task's created file ID for idempotent retries.
10. Downloads the committed object from the pool and recomputes its source
    metadata: SHA-256 hash plus the same EXIF / dimensions / blurhash / media
    probe analysis the proxied flow runs on the staged file. The media type
    is resolved authoritatively from the downloaded bytes (the declared type
    is trusted unless it is the generic `application/octet-stream`, in which
    case the bytes are sniffed) and persisted on the `FileObject`, so direct
    uploads whose presigned `PUT` omitted `Content-Type` still land with the
    correct type and derivative flags. The local `FileObject` record is
    overwritten with the results so direct uploads carry identical
    first-persisted metadata. The analysis also derives the compatibility
    flags from the resolved media type — images are flagged for a compression
    derivative, videos for a thumbnail — so the file reports its expected
    real state instead of placeholders. This step is best-effort: if the
    download or analysis fails, the upload still succeeds and the file is
    returned with whatever metadata it has.
11. Publishes the upload processing event and file metadata update event.
12. Returns the visible file with `status: 2`.

Example response shape:

```json
{
  "id": "01J...",
  "name": "photo.jpg",
  "mime_type": "image/jpeg",
  "size": 5242880,
  "status": 2,
  "has_compression": true,
  "has_thumbnail": false
}
```

The complete operation is idempotent after the file ID has been stored. A
repeated request returns the previously created file rather than creating a
second file.

If the object is missing or its size does not match, the endpoint returns an
error and does not create a visible file. The task remains available for the
client to retry while it is still `Uploading`.

## Processing

The existing upload worker consumes the `filesystem.file.uploaded.v1` processing event. It
loads the source from the file's storage backend when no local processing path
is present.

For image and video files, the worker can create derived files such as:

- `system.compression.low` for compressed image output
- `system.thumbnail` for video thumbnails

After successful processing, the worker recomputes the compatibility flags
from the actual derivative children and changes the file and task to
`Completed`, then publishes the final metadata snapshot with the real flags.
On an error, it recomputes the flags (no derivatives exist), changes the file
and task to `Failed`, and stores the processing error on the upload task.

Clients can query the task state with:

```http
GET /api/files/upload/status/<task-id>
Authorization: Bearer <session-token>
```

Response:

```json
{
  "task_id": "01J...",
  "status": 3,
  "progress": 1,
  "file_id": "01J...",
  "error": null
}
```

The `error` field contains the processing error when `status` is `Failed`.

## Events

### Processing event

The direct completion path publishes the processing event:

```text
filesystem.file.uploaded.v1
```

Its payload includes the shared-style event envelope plus the file ID, task ID,
content type, storage key, and the source processing path when one exists. All
DysonFS worker instances consume this subject from the durable
`dysonfs_file_processing` JetStream consumer. The queue group distributes each
upload to one processing worker instance.

### File metadata update event

DysonFS also publishes lifecycle metadata updates on:

```text
filesystem.file.updated.v1
```

The event uses the `filesystem_events` JetStream stream and contains an event
envelope plus a file metadata snapshot:

```json
{
  "event_id": "01J...",
  "timestamp": "2026-07-31T23:00:00Z",
  "event_type": "filesystem.file.updated.v1",
  "stream_name": "filesystem_events",
  "file_id": "01J...",
  "task_id": "01J...",
  "account_id": "account-id",
  "status": 2,
  "file": {
    "id": "01J...",
    "name": "photo.jpg",
    "mime_type": "image/jpeg",
    "size": 5242880,
    "has_compression": true,
    "has_thumbnail": false,
    "status": 2,
    "updated_at": "2026-07-31T23:00:00Z"
  }
}
```

The event is intended for DysonNetwork services such as Sphere. Those
services can use `file_id` to update denormalized JSONB file-reference
snapshots when the status or derived metadata changes.

Consumers must tolerate duplicate events and should not assume that events are
delivered exactly once. They should compare the snapshot's `updated_at` with
the stored reference before applying an older event.

## Multipart Direct Upload

For larger files, the client can request a multipart session instead of a
single presigned `PUT`. The server creates an S3 multipart upload, presigns
one `PUT` URL per part on demand, and completes the session server-side with
authoritative verification of the uploaded parts.

### 1. Prepare

Set `multipart: true` in the same `POST /api/files/upload/prepare` request
used for single-PUT uploads:

```json
{
  "file_name": "movie.mov",
  "file_size": 10737418240,
  "content_type": "video/quicktime",
  "multipart": true,
  "pool_id": "pool-id"
}
```

On success the response carries the session and part plan instead of a single
`upload_url`:

```json
{
  "task_id": "01J...",
  "status": 1,
  "object_key": "uploads/01J.../source",
  "upload_id": "QmFzZTY0Li4u",
  "part_size": 5242880,
  "part_count": 2048,
  "expires_in": 900,
  "content_type": "video/quicktime"
}
```

`part_size` is 5 MiB (5242880 bytes); `part_count` is derived from the
declared file size. Parts are numbered 1 through `part_count`, and every part
except the last is exactly `part_size` bytes. Pools that cannot presign
multipart part URLs are rejected with the same
`{ "error": ..., "use_proxied_upload": true }` response as unsupported direct
uploads; the client should fall back to the proxied chunked flow.

### 2. Presign a part

Issue one presigned URL per part, on demand:

```http
POST /api/files/upload/<task-id>/part
Authorization: Bearer <session-token>
Content-Type: application/json
```

```json
{ "part_number": 7 }
```

Response:

```json
{
  "part_number": 7,
  "upload_url": "https://s3.example.com/bucket/uploads/01J.../source?partNumber=7&uploadId=...&X-Amz-...",
  "expires_in": 900,
  "content_type": "video/quicktime"
}
```

The URL is valid for 15 minutes and authorizes exactly one `PUT` of that part:

```http
PUT <upload_url>
Content-Type: video/quicktime

<part bytes>
```

Requesting parts one at a time (instead of materializing every URL in prepare)
keeps responses small and makes resume natural: after an interruption the
client re-requests the parts it still needs. The endpoint validates task
ownership, the `Uploading` status, and that `part_number` falls within
`1..part_count`.

### 3. Complete

`POST /api/files/upload/<task-id>/complete` behaves as described in the
single-PUT flow, with one extra step for multipart sessions: before the `Stat`
check, the server lists the uploaded parts, verifies the count matches
`part_count`, the part numbers are contiguous `1..N`, and the part sizes sum
exactly to the declared `file_size`, then calls S3's complete-multipart
operation. After completion the object exists under the same `object_key` and
the normal verification and file-creation steps run unchanged, including the
post-commit download and source-metadata analysis described above.

If parts are missing or sizes do not match, the endpoint returns a 400 error
and leaves the task `Uploading` so the client can upload the missing parts and
retry. The session is only discarded explicitly, via cancel:

```http
DELETE /api/files/upload/<task-id>
Authorization: Bearer <session-token>
```

Cancel aborts the multipart session (discarding uploaded parts) and marks the
task failed.

### 4. Expiry

Uploads that never complete (a client that crashed or abandoned the session)
would otherwise leave multipart sessions and single-PUT objects in the storage
pool forever. Every hour the server sweeps upload tasks still in `Uploading`
whose last activity is older than six hours and:

1. Claims the task atomically — a completion that raced the sweep wins and the
   task is left untouched.
2. Aborts the multipart session if one exists, so S3 discards the uploaded
   parts.
3. Deletes the object under `object_key` (single-PUT objects; a no-op for
   incomplete multipart sessions).
4. Marks the task `expired`/failed with `processing_error` explaining the
   expiry; the 30-day stale-task purge later removes the row.

Part presign requests refresh the task's activity, so a session that is still
uploading parts is never expired — only genuinely idle sessions are.

Admins can run the same sweep on demand instead of waiting for the hourly
pass:

```http
POST /api/admin/uploads/gc
Authorization: Bearer <session-token>
```

Access requires the same permission as the other admin storage endpoints
(superuser or `files.manage`); the response reports how many tasks expired.

## Proxied Fallback

Clients should use the existing proxied endpoints when direct upload is not
available:

### Single request

```http
POST /api/files/upload/direct
Content-Type: multipart/form-data
```

### Chunked request

```http
POST /api/files/upload/create
POST /api/files/upload/chunk/<task-id>/<chunk-index>
POST /api/files/upload/complete/<task-id>
```

These endpoints continue to send file data through DysonFS and are appropriate
for local pools, unsigned pools, or clients that require the existing resumable
chunk behavior.

## Security Rules

- The client cannot choose the S3 object key.
- The signed URL is short-lived.
- Prepare validates upload permission before issuing a URL.
- Workspace membership and workspace quota are checked before issuing a URL.
- Pool policy and file-size limits are checked before issuing a URL.
- Complete verifies the remote object exists in the expected pool.
- Complete verifies the remote object size against the declared size.
- Task ownership is checked on completion and status requests.
- A signed URL alone does not create a DysonFS file.

## Current Limitations

- Multipart direct uploads require an S3-compatible pool that supports
  multipart presigning (all S3 backends do); local and unsigned pools fall
  back to proxied uploads.
- The client tracks which parts it has uploaded. The server verifies part
  completeness only at completion time; there is no per-part status endpoint.
- The DysonFS processing subject is JetStream-backed and uses the shared-style
  `filesystem.file.uploaded.v1` event envelope.
- File metadata updates use JetStream, but a transactional outbox is not yet
  implemented. A process failure between the database commit and event
  publication can require reconciliation.
- The shared C# event type and Sphere listener still need to be added in
  `DysonNetwork.Shared` and `DysonNetwork.Sphere`.
- Existing legacy files may have status `Unknown` until they are rewritten or
  migrated; new upload tasks and newly created files receive an explicit
  lifecycle status.
