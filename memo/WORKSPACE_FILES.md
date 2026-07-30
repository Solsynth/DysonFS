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

## Browsing workspace files and quota

Personal listing routes deliberately query only files where `workspace_id` is
unset. To browse a workspace, explicitly select it with `workspace_id`:

```text
GET /api/files/root/children?workspace_id=WORKSPACE_ID
GET /api/files/unindexed?workspace_id=WORKSPACE_ID
GET /api/files/:folderId/children?workspace_id=WORKSPACE_ID
```

Direct file reads (`GET /api/files/:fileId`) and metadata lookups
(`GET /api/files/meta?ids=FILE_ID`) are ID-based and may resolve either
personal or workspace files without `workspace_id`.

Workspace membership is verified before these queries run. View its storage
limit and current usage with:

```text
GET /api/billing/workspaces/WORKSPACE_ID/quota
```

The response contains `used_bytes`, `total_bytes`, `remaining_bytes`, and
`total_file_count`.

## WebDAV

WebDAV uses the personal namespace by default. Select a workspace by adding
`workspace_id` to the WebDAV URL:

```text
https://files.example.com/webdav/?workspace_id=WORKSPACE_ID
```

DysonFS verifies active workspace membership before serving the request. All
WebDAV listing, path resolution, folder creation, and uploaded files stay in
the selected workspace namespace. Omitting `workspace_id` continues to use
the authenticated user's personal files.

Workspace uploads can be indexed or unindexed, independently of personal
files. Omit `index` (or set it to `false`) when uploading to create an
unindexed workspace file, then list it with
`GET /api/files/unindexed?workspace_id=WORKSPACE_ID`. Setting `index` to
`true`, or uploading beneath an indexed workspace folder, places it in that
workspace's normal file tree.

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

An overwrite always keeps the target file's workspace ownership. Supply the
target workspace's `workspace_id` when overwriting a workspace file; omitting
it searches only personal files. A different workspace ID is rejected with
`400 Bad Request`.
