# Admin storage API

This document describes every administrative endpoint currently exposed by
DysonFS. These endpoints are rooted at `/api/admin/storage`.

## Authorization

Every endpoint requires a bearer token. The caller must be a superuser or have
the Padlock `files.manage` permission. A missing token returns `401`; a token
without either form of access returns `403`.

```http
Authorization: Bearer <token>
```

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/config` | List every pool's storage configuration, with secrets redacted. |
| `PATCH` | `/config/:poolId` | Replace a pool's storage configuration. |
| `GET` | `/status` | List registered storage nodes and their current health. |
| `GET` | `/health` | Return an aggregate storage-node health summary. |
| `GET` | `/stats` | Return file counts and stored bytes per pool. |
| `GET` | `/failures` | Return recent in-memory server failure events. |
| `POST` | `/pool-migrations` | Queue a durable task to move files from one pool to another. |
| `GET` | `/pool-migrations/:taskId` | Retrieve a pool-migration task and its progress. |

## Pool storage configuration

### `GET /api/admin/storage/config`

Lists all pools. `storage_config.secret_id` and `storage_config.secret_key` are
always blank in the response; the adjacent `*_configured` flags tell whether a
secret is already present.

```json
[
  {
    "id": "pool-id",
    "name": "Primary",
    "description": "Primary object storage",
    "storage_config": {
      "endpoint": "s3.example.com",
      "bucket": "files",
      "enable_ssl": true,
      "enable_signed": true,
      "secret_id": "",
      "secret_key": ""
    },
    "secret_id_configured": true,
    "secret_key_configured": true,
    "billing_config": {"cost_multiplier": 1},
    "policy_config": {"public_usable": false},
    "is_hidden": false
  }
]
```

### `PATCH /api/admin/storage/config/:poolId`

Replaces the pool's `storage_config`. The supplied configuration is validated
before it is saved. Omit `secret_id` and `secret_key` (or send them as empty
strings) to retain the existing credentials.

```json
{
  "storage_config": {
    "endpoint": "s3.example.com",
    "bucket": "files-next",
    "enable_ssl": true,
    "enable_signed": true
  }
}
```

Returns the same redacted pool configuration shape as `GET /config`.

## Storage node health

### `GET /api/admin/storage/status`

Returns the registered storage nodes and a health evaluation. A node is healthy
when its status is `online` and it has sent a heartbeat in the past two
minutes.

```json
{
  "checked_at": "2026-07-30T12:00:00Z",
  "nodes": [
    {
      "id": "node-id",
      "name": "storage-1",
      "machine_id": "machine-1",
      "endpoint": "https://storage-1.example.com",
      "pool_id": "pool-id",
      "status": "online",
      "healthy": true,
      "last_seen_at": "2026-07-30T11:59:45Z"
    }
  ]
}
```

### `GET /api/admin/storage/health`

Returns `healthy` when every registered node is healthy, `degraded` when at
least one is healthy, and `unhealthy` when no nodes are healthy or no nodes are
registered.

```json
{
  "status": "healthy",
  "checked_at": "2026-07-30T12:00:00Z",
  "total_nodes": 2,
  "healthy_nodes": 2
}
```

## Usage and failures

### `GET /api/admin/storage/stats`

Returns counts of live file rows and the sum of their object sizes, grouped by
pool. Files without a pool are reported as `default`.

```json
{
  "calculated_at": "2026-07-30T12:00:00Z",
  "pools": [{"pool_id": "pool-id", "file_count": 42, "used_bytes": 1048576}]
}
```

### `GET /api/admin/storage/failures?limit=100`

Returns the process-local server failure log. `limit` is optional and must be
between `1` and `100`; events are newest first. This log and its counters are
cleared when the process restarts. See [Server failure events](SERVER_FAILURE_EVENTS.md)
for the response schema and retention details.

## Pool migrations

### `POST /api/admin/storage/pool-migrations`

Creates a durable `pool.migration` task. Source and target pools must exist and
must be different. `file_ids` is optional: omit it to migrate every file in the
source pool, or provide it to migrate only the listed files. Each listed file
must belong to the source pool.

```json
{
  "source_pool_id": "source-pool-id",
  "target_pool_id": "target-pool-id",
  "file_ids": ["file-id-1", "file-id-2"]
}
```

The API returns `202 Accepted` with the persistent task. Workers claim pending
tasks atomically, copy each object into the target storage backend, update its
file record to use the target pool, and remove an unreferenced source object.
If a worker stops, a task left in `processing` is eligible to be picked up again
after 15 minutes.

### `GET /api/admin/storage/pool-migrations/:taskId`

Returns the task created by the previous endpoint. Relevant fields are:

| Field | Meaning |
| --- | --- |
| `status` | `pending`, `processing`, `completed`, or `failed`. |
| `progress` | Fraction from `0` through `1`. |
| `chunks_count` | Number of source file records found when the task was queued. |
| `chunks_uploaded` | Number of file records moved so far. |
| `parameters.source_pool_id` | Source pool for the migration. |
| `parameters.target_pool_id` | Destination pool for the migration. |
| `parameters.file_ids` | Requested file IDs; omitted when the whole source pool was selected. |
| `error_message` | Populated when a migration fails. |

Use this endpoint to poll progress until the task is `completed` or `failed`.
