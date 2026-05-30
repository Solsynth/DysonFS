# WebDAV Support

DysonFS exposes indexed files over WebDAV, allowing clients to mount, browse, and manage files as a remote drive.

## Configuration

Enable WebDAV in your TOML config:

```toml
[webdav]
enabled = true
prefix = "/webdav"
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the WebDAV endpoint |
| `prefix` | `/webdav` | URL path prefix for the WebDAV endpoint |

## Authentication

WebDAV clients authenticate using HTTP Basic Auth. The **username is ignored** and the **password is the token** (an API key or auth key created through the Solar Network auth system).

```
Authorization: Basic base64(username:token)
```

The server decodes the Basic Auth header and converts it to a Bearer token for validation against the auth service.

## Supported Operations

| Method | Description |
|--------|-------------|
| `PROPFIND` | List directory contents or get file properties |
| `GET` / `HEAD` | Download file content |
| `PUT` | Upload or overwrite a file |
| `DELETE` | Delete a file or directory |
| `MKCOL` | Create a directory |
| `COPY` | Copy a file or directory |
| `MOVE` | Move or rename a file or directory |
| `LOCK` / `UNLOCK` | Lock/unlock a file for exclusive editing |

## Locking

Files can be locked via WebDAV's `LOCK` and `UNLOCK` methods. Locks are unified across protocols — a file locked via WebDAV is also locked for WOPI (Collabora) and vice versa. This prevents conflicting writes from different access methods.

- Lock timeout defaults to 30 minutes
- Locks are automatically cleaned up after expiry
- Lock tokens use the `urn:uuid:` format

## Client Examples

### macOS Finder

1. Open Finder
2. Go → Connect to Server (⌘K)
3. Enter: `http://localhost:8080/webdav/`
4. Enter credentials (username: anything, password: your token)

### Windows Explorer

1. Open File Explorer
2. Right-click "This PC" → "Map network drive"
3. Enter: `http://localhost:8080/webdav/`
4. Check "Connect using different credentials"
5. Enter credentials (username: anything, password: your token)

### Linux (GVfs / Nautilus)

1. Open Files
2. Click "+ Other Locations"
3. In the connect bar: `davs://localhost:8080/webdav/`
4. Enter credentials when prompted

### rclone

Create an rclone remote config:

```ini
[dysonfs]
type = webdav
url = http://localhost:8080/webdav/
user = anything
pass = your_token_base64_encoded
```

Then mount:

```bash
rclone mount dysonfs: /mnt/dysonfs --vfs-cache-mode full
```

### cadaver (CLI)

```bash
cadaver http://localhost:8080/webdav/
```

Enter credentials when prompted.

## Path Mapping

WebDAV paths map directly to the DysonFS indexed file tree:

| WebDAV Path | DysonFS Location |
|-------------|-----------------|
| `/webdav/` | Root of user's indexed files |
| `/webdav/Documents/` | Folder named "Documents" at root |
| `/webdav/Documents/report.pdf` | File "report.pdf" inside "Documents" |

Path resolution walks the file tree from root, matching each segment by name. Only indexed files are visible.

## Limitations

- **Partial uploads**: `PUT` writes the entire file. Range-based partial writes are not supported.
- **File size**: Limited by available disk space for temp files and pool quotas.
- **Concurrent editing**: Use `LOCK`/`UNLOCK` to prevent conflicts. The unified lock system ensures WebDAV and WOPI locks don't conflict.
- **Derived content**: Thumbnails and compressed variants are generated asynchronously after upload via the worker pipeline.
