-- DysonFS key-scheme migration. Run AFTER the rclone bucket scripts:
--   1. scripts/prefix-legacy-keys.sh   (bucket: legacy keys -> uploads/)
--   2. scripts/move-cache-uploads.sh   (bucket: cache uploads/ -> main uploads/)
--
-- Run:  psql "$DATABASE_DSN" -f scripts/migrate-keys.sql
-- Pool ids for section B are set via \set below; edit them or override:
--   psql -v cache_pool=01CACHE... -v default_pool=01SYSTEM... -f scripts/migrate-keys.sql
--
-- The whole migration is one transaction; nothing is committed if any step fails.

BEGIN;

-- ---------------------------------------------------------------------------
-- A. Prefix every legacy (unprefixed) storage key with uploads/
-- ---------------------------------------------------------------------------
-- Mirrors prefix-legacy-keys.sh. file_objects and cloud_files must stay in
-- sync: both columns reference the same object key.
UPDATE file_objects
SET storage_key = 'uploads/' || storage_key
WHERE storage_key IS NOT NULL
  AND storage_key NOT LIKE 'uploads/%'
  AND storage_key NOT LIKE 'media-cache/%';

UPDATE cloud_files
SET storage_key = 'uploads/' || storage_key
WHERE storage_key IS NOT NULL
  AND storage_key NOT LIKE 'uploads/%'
  AND storage_key NOT LIKE 'media-cache/%';

-- ---------------------------------------------------------------------------
-- B. Repoint records that were misrouted into the cache pool
-- ---------------------------------------------------------------------------
-- After move-cache-uploads.sh the objects live in the main bucket. Records
-- whose storage_id/pool_id name the cache pool must name the default pool,
-- or openFile still resolves the cache bucket and 404s.
\set cache_pool REPLACE_WITH_CACHE_POOL_ID
\set default_pool REPLACE_WITH_DEFAULT_POOL_ID

UPDATE cloud_files
SET storage_id = :'default_pool', pool_id = :'default_pool'
WHERE storage_id = :'cache_pool' OR pool_id = :'cache_pool';

COMMIT;

-- ---------------------------------------------------------------------------
-- Verification (after COMMIT; run in a fresh session if psql aborts)
-- ---------------------------------------------------------------------------

-- Expect 0: any unprefixed key left behind means the bucket rename was
-- incomplete for those rows.
SELECT count(*) AS legacy_keys_remaining
FROM file_objects
WHERE storage_key IS NOT NULL
  AND storage_key NOT LIKE 'uploads/%'
  AND storage_key NOT LIKE 'media-cache/%';

-- Expect 0: any record still pointing at the cache pool.
SELECT count(*) AS cache_pool_records_remaining
FROM cloud_files
WHERE storage_id = :'cache_pool' OR pool_id = :'cache_pool';

-- Manual review only: rows with no storage key rely on the object_id
-- fallback, which is NOT prefixed by this migration. If any show up and the
-- object lives under uploads/, set the key explicitly:
--   UPDATE cloud_files SET storage_key = 'uploads/' || object_id
--   WHERE id IN (SELECT id FROM cloud_files WHERE storage_key IS NULL AND object_id IS NOT NULL);
SELECT id, object_id, storage_key
FROM cloud_files
WHERE storage_key IS NULL AND object_id IS NOT NULL;
