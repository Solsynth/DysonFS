# DysonFS

aka. Dyson Network File System

Go implementation of the Dyson Network file service.

## Modes

- `master`: HTTP API, gRPC, uploads, file serving, health check
- `worker`: post-upload media processing, derived file generation, cleanup
- `storage`: optional local storage node for filesystem-backed deployments

## CLI

Use the first positional argument as the command.

```bash
go run ./cmd master
go run ./cmd migrate-legacy --config ./config.toml --legacy-dsn "$LEGACY_DATABASE_DSN"
go run ./cmd reanalyze-missing --config ./config.toml
go run ./cmd validate-storage --config ./config.toml
```

## Logging

- `ZEROLOG_PRETTY=true` enables console-style pretty logs
- `LOG_LEVEL=debug|info|warn|error` sets the log level

## Run

```bash
go run ./cmd master
```

### Legacy migration

Use the one-shot migrator to import data from the old C# database into the new schema:

```bash
go run ./cmd migrate-legacy --config ./config.toml --legacy-dsn "$LEGACY_DATABASE_DSN"
```

Flags:

- `--dry-run` to simulate without writing
- `--skip-derived` to skip thumbnail/compression child reconstruction
- `--batch-size` to tune import batch size
- `--continue-on-error` to keep going after row-level failures

### Metadata reanalysis

Repair missing image/video metadata from stored source files:

```bash
go run ./cmd reanalyze-missing --config ./config.toml
```

It shows a preview first, then asks for confirmation before changing anything.

Flags:

- `--reanalyze-limit` to cap the preview/repair batch size
- `--file-id` to target one or more file IDs, comma-separated
- `--preview-count` to control how many candidates are shown first
- `--yes` to skip the confirmation prompt

### Upload API

Both direct upload and chunked upload creation accept the same metadata payload:

```json
{
  "hash": "...",
  "file_name": "clip.mov",
  "file_size": 12345,
  "content_type": "video/quicktime",
  "pool_id": "...",
  "expired_at": "2026-05-17T12:34:56Z",
  "chunk_size": 5242880,
  "parent_id": "...",
  "usage": "...",
  "application_type": "..."
}
```

- `direct` upload uses multipart form data with the same field names, plus `file`
- `parent_id` is optional and can still be resolved server-side when omitted
- `hash` is stored on the created file/task when provided
- upload quota is checked before task creation or direct upload processing
- quota refusal returns `403 Forbidden`
- if `pool_id` is omitted, the quota check uses the resolved default pool

### Quota And Billing

Quota values are reported in MB.

Base quota is the sum of:

- leveling quota
- perk quota

Leveling quota uses `account.profile.level`, clamped to `0..120`, with these milestones:

- `Lv0`: `512MB`
- `Lv10`: `1GB`
- `Lv60`: `5GB`
- `Lv120`: `10GB`

Between those milestones, the quota is interpolated piecewise.

Perk quota is added on top of leveling quota:

- perk `1`: `10GB`
- perk `2`: `25GB`
- perk `3`: `50GB`

Extra quota comes from active `quota_records` and is added after the base quota.

`GET /api/billing/quota` returns:

```json
{
  "based_quota": 15360,
  "extra_quota": 25,
  "total_quota": 15385
}
```

`GET /api/billing/usage` returns:

```json
{
  "used_quota": 300,
  "total_quota": 15385,
  "total_file_count": 2,
  "total_usage_bytes": 209715200
}
```

Usage accounting rules:

- `used_quota` is billable usage in MB, not raw bytes
- raw file bytes are returned separately as `total_usage_bytes`
- pool billing `cost_multiplier` affects billable usage and quota checks
- the multiplier is applied per file based on the file's pool

### Folders

Folders are created with `POST /folders`.

Request body:

```json
{
  "name": "Projects",
  "parent_id": "..."
}
```

- A folder is stored as a `cloud_files` row with `is_folder = true` and `indexed = true`
- `parent_id` is optional
- The current implementation does not yet validate that the parent exists or is a folder
- Root folders automatically get a private read permission record

### Permission Management

Files expose read/write/manage permissions through `GET /files/:id/permissions` and `PUT /files/:id/permissions`.

- No file permission rows means the file is public
- A `private` permission row with `read` makes a file private by default
- Permission checks inherit from ancestor folders
- `PUT /files/:id/permissions` replaces the full permission set in one batch

Request body:

```json
{
  "items": [
    {
      "id": "...",
      "file_id": "...",
      "subject_type": "account",
      "subject_id": "...",
      "permission": "read"
    },
    {
      "id": "...",
      "file_id": "...",
      "subject_type": "scope",
      "subject_id": "files.manage",
      "permission": "manage"
    }
  ]
}
```

- `subject_type` can be `public`, `private`, `account`, or `scope`
- `permission` is typically `read`, `write`, or `manage`
- Send the full desired list; omitted rows are removed

### Batch File Operations

Batch operations use `POST /api/files/<operation>/batch` with a JSON body.

Move files into a parent:

```json
{
  "file_ids": ["..."],
  "parent_id": "..."
}
```

Available operations:

- `POST /api/files/recycle/batch`
- `POST /api/files/restore/batch`
- `POST /api/files/delete/batch`
- `POST /api/files/move/batch`

Notes:

- `file_ids` is required for every batch operation
- `parent_id` is optional for `move`; omit it or set it to `null` to move files back to the root
- `POST /api/files/batches/delete` remains as a compatibility alias for batch recycle

### File Listings

List responses include extra metadata for navigation and access UI:

- `children_count` for immediate child count
- `permission_status` for current access state

Performance notes:

- Child counts and inherited permission status are resolved in batches for list responses to avoid per-file query fan-out.
- Postgres should have a composite index on `cloud_files(parent_id, deleted_at)` so `children_count` lookups do not fall back to full table scans.
- Postgres should have a composite index on `file_permissions(file_id, permission, deleted_at)` so inherited permission-source lookups stay index-backed.
- These indexes are declared in the GORM models and are created by `AutoMigrate`, but existing deployments need a restart or migration run before the new indexes appear.
- If file-list or permission logs still show slow SQL after rollout, verify the indexes exist with `pg_indexes` and inspect the hot queries with `EXPLAIN (ANALYZE, BUFFERS)`.

Example:

```json
"permission_status": {
  "readable": true,
  "writable": false,
  "manageable": false,
  "visibility": "private",
  "inherited_from": "..."
}
```

### Storage validation

Validate `file_objects.storage_key` against remote S3 objects and clean up orphans:

```bash
go run ./cmd validate-storage --config ./config.toml --yes
```

It snapshots remote keys first, then compares the snapshot against the database in batches.

Flags:

- `--validate-snapshot` to choose the snapshot file path
- `--validate-prefix` to limit the remote listing prefix
- `--validate-batch` to control DB batch size
- `--yes` to skip the confirmation prompt

## Config

Use `--config` or `CONFIG_PATH` for a TOML config file.

Key settings:

- `app.name`
- `database.dsn`
- `http.port`
- `grpc.port`
- `grpc.useTLS`
- `grpc.certFile`
- `grpc.keyFile`
- `storage.tempDir`
- `storage.localDir`
- `auth.target`
- `auth.useTLS`
- `passport.target`
- `passport.useTLS`
- `quota.leveling.level1`
- `quota.leveling.level10`
- `quota.leveling.level60`
- `quota.leveling.level120`
- `nats.url`
- `mode.master`
- `mode.worker`
- `mode.storage`

Pool storage is configured with `[[pools]]` and seeded into the database at startup.

Example:

```toml
[[pools]]
id = "500e5ed8-bd44-4359-bc0a-ec85e2adf447"
name = "Default"
default = true
hidden = false

[pools.storage]
endpoint = "http://minio:9000"
bucket = "dyson-files"
enableSigned = true
enableSsl = false
secretId = "minio"
secretKey = "minio123"
```

## Notes

- Public read is the default.
- Explicit ACL rows restrict access when present.
- `master` resolves storage from pool config stored in the database.
- `worker` listens for file upload events and builds thumbnails, blurhash, and other derived artifacts.
- `master` can use S3 directly; local storage is still supported.
- The Docker image expects `ffmpeg` and `libvips` runtime packages.
- The E2EE file route has been removed.
