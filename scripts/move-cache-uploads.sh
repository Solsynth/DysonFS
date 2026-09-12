#!/usr/bin/env bash
# Move every uploads/* object from the cache bucket back to the main bucket.
# These are user files that were misrouted into the cache bucket; media-cache/*
# stays put.
#
# Usage: ./move-cache-uploads.sh [remote] [main-bucket] [cache-bucket]
#   remote       rclone remote name (default: r2)
#   main-bucket  main bucket (default: solar-network)
#   cache-bucket cache bucket (default: solar-network-cache)
#
# Requires a DB migration in lockstep: run scripts/migrate-keys.sql (repoints
# records from the cache pool to the default pool) or the files 404.
set -euo pipefail

REMOTE="${1:-r2}"
MAIN="${2:-solar-network}"
CACHE="${3:-solar-network-cache}"

echo "objects in ${CACHE}/uploads/:"
rclone lsf "${REMOTE}:${CACHE}/uploads/" --files-only | wc -l

rclone move "${REMOTE}:${CACHE}/uploads/" "${REMOTE}:${MAIN}/uploads/" \
  --fast-list -P --dry-run

read -r -p "proceed? [y/N] " ans
[[ "$ans" =~ ^[Yy]$ ]] || exit 1

rclone move "${REMOTE}:${CACHE}/uploads/" "${REMOTE}:${MAIN}/uploads/" \
  --fast-list -P

echo "done; verify with:"
echo "  rclone lsf ${REMOTE}:${CACHE}/ --files-only   # want only media-cache/*"
echo "  rclone lsf ${REMOTE}:${MAIN}/uploads/ --files-only | wc -l"
