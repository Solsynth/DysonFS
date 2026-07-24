# Workspace files

DysonFS can store files and folders for a workspace. A workspace file retains
the account that uploaded it for audit purposes, while its `workspace_id`
identifies the workspace that owns it. Storage usage for these files is charged
to the workspace plan, never to the uploader's personal quota.

## Prerequisites

Configure the WattEngine `DyWorkspaceService` endpoint:

```toml
[workspace]
target = "watt-engine:5001"
useTLS = false
tlsSkipVerify = false
```

Workspace uploads are rejected when this endpoint is not configured.

## Authorization and quota

For every workspace upload, DysonFS verifies through `DyWorkspaceService` that
the authenticated account is an active workspace member with role `Member`
(50) or higher. It loads the workspace plan and compares existing live file
usage plus the requested upload size with `max_storage_bytes` from
`GetPlanQuota`.

When the limit would be exceeded, the API responds with `403 Forbidden`. This
check is performed when a chunked task is created and again when it completes,
so a plan change or concurrent uploads cannot bypass the limit. Personal files,
which omit `workspace_id`, retain the existing account quota behavior.

## Creating folders

Create a workspace folder with `POST /api/files/folders`:

```json
{
  "name": "Design assets",
  "workspace_id": "b3d7f2d1-4a3c-4682-81b4-bccdfd69e03e"
}
```

`parent_id` is optional. If present, the parent must be a folder in the same
workspace; a workspace file or folder cannot be placed beneath a personal
folder, or vice versa.

## Direct upload

Use `workspace_id` as a multipart form field:

```bash
curl -X POST "https://files.example.com/api/files/upload/direct" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -F "file=@./architecture.pdf" \
  -F "workspace_id=b3d7f2d1-4a3c-4682-81b4-bccdfd69e03e" \
  -F "parent_id=FOLDER_ID" \
  -F "index=true"
```

The resulting file response includes `workspace_id`.

## Chunked upload

Include `workspace_id` when creating the task:

```json
POST /api/files/upload/create

{
  "file_name": "recording.mp4",
  "file_size": 104857600,
  "content_type": "video/mp4",
  "chunk_size": 5242880,
  "workspace_id": "b3d7f2d1-4a3c-4682-81b4-bccdfd69e03e",
  "parent_id": "FOLDER_ID"
}
```

The workspace ID is stored with the upload task and applied to the created
file when `POST /api/files/upload/complete/:taskId` succeeds.

## Overwrites

An overwrite always keeps the target file's workspace ownership. Supplying a
different `workspace_id` is rejected with `400 Bad Request`; omitting it is
safe because DysonFS derives the workspace from the target file.
