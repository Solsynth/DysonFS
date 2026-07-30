# Server failure events

DysonFS records HTTP responses with a status of 500 or greater in an in-memory
failure log. This is intended for short-lived operational diagnosis; it is not
a durable audit-log or alerting system.

## Retention and counters

The server retains the newest 100 detailed events. When the limit is reached,
the oldest event is discarded. Two counters are maintained for the lifetime of
the process and do not decrease when an event is discarded:

- `server_failure_count`: every recorded HTTP 5xx response.
- `upload_failure_count`: recorded 5xx responses whose path starts with
  `/api/files/upload/`.

Restarting the server clears both the detailed events and the counters.

Each event includes its timestamp, HTTP method, request path, response status,
whether it is an upload failure, and up to 4 KiB of error-response detail. The
logger does not retain request bodies or query strings, preventing uploaded
content, tokens, and query parameters from being copied into the event log.

## Admin API

Retrieve the counters and recent events with:

```http
GET /api/admin/storage/failures?limit=100
Authorization: Bearer <token>
```

The endpoint requires the Padlock `files.manage` permission node. Superusers
bypass the permission check. `limit` is optional and must be from 1 through
100; events are returned newest first.

Example response:

```json
{
  "capacity": 100,
  "retained_event_count": 2,
  "server_failure_count": 14,
  "upload_failure_count": 5,
  "events": [
    {
      "occurred_at": "2026-07-30T06:12:00Z",
      "method": "POST",
      "path": "/api/files/upload/direct",
      "status": 500,
      "detail": "{\"error\":\"storage unavailable\"}",
      "upload_failure": true
    }
  ]
}
```

## Operational guidance

Use `upload_failure_count` to spot upload-specific server errors and inspect
the matching recent events for the endpoint and server-provided error detail.
For persistent observability, export application logs or metrics to your
monitoring system; this in-memory endpoint is intentionally bounded and is
cleared on restart.
