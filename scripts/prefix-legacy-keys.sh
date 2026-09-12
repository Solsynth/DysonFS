#!/usr/bin/env bash
# Prefix every legacy (non-uploads/, non-media-cache/) object in the main
# bucket with uploads/ so the bucket namespace matches the server's key scheme.
#
# Usage: ./prefix-legacy-keys.sh [remote] [main-bucket]
#   remote      rclone remote name (default: r2)
#   main-bucket main bucket (default: solar-network)
#
# Requires a DB migration in lockstep: run scripts/migrate-keys.sql (prefixes
# file_objects.storage_key and cloud_files.storage_key with uploads/) or the
# files 404.
set -euo pipefail

REMOTE="${1:-r2}"
MAIN="${2:-solar-network}"
LIST="${TMPDIR:-/tmp}/dyson-legacy-keys.txt"

rclone lsf "${REMOTE}:${MAIN}/" --files-only |
  grep -v '^uploads/' |
  grep -v '^media-cache/' > "$LIST"

count=$(wc -l < "$LIST" | tr -d ' ')
if [ "$count" -eq 0 ]; then
  echo "nothing to prefix in ${MAIN}/"
  exit 0
fi
echo "found $count legacy keys to prefix in ${MAIN}/"

rclone move "${REMOTE}:${MAIN}/" "${REMOTE}:${MAIN}/uploads/" \
  --files-from "$LIST" --fast-list -P --dry-run

read -r -p "proceed? [y/N] " ans
[[ "$ans" =~ ^[Yy]$ ]] || exit 1

rclone move "${REMOTE}:${MAIN}/" "${REMOTE}:${MAIN}/uploads/" \
  --files-from "$LIST" --fast-list -P

echo "done; verify with:"
echo "  rclone lsf ${REMOTE}:${MAIN}/ --files-only | grep -vc '^uploads/'"
