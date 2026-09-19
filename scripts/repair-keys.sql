-- DysonFS key-scheme repair (transactional, idempotent).
--
-- Repairs records the migration mis-keyed: it resolved a record's bucket from
-- the pool owning `solar-network`, while the server resolves it from the pool
-- marked `default = true` in config.toml (internal/service BackendForFile).
-- Where those differ, rows with NULL pool_id were prefixed with
-- uploads/ although their bucket was never renamed.
--
-- Preview:  psql "$DATABASE_DSN" -v default_pool=<id> -f scripts/repair-keys.sql
-- Apply:    psql "$DATABASE_DSN" -v default_pool=<id> -v apply=yes -f scripts/repair-keys.sql
--
-- default_pool is REQUIRED: guessing it is what caused the damage.
--
-- The bucket listing at /tmp/dysonfs-remote-keys.txt is the only proof of which
-- key form actually exists on the remote. Build it (create the file first, it
-- must exist even when empty) with, for the bucket of the default pool:
--   touch /tmp/dysonfs-remote-keys.txt
--   rclone lsf -R --files-only "REMOTE:BUCKET/" >> /tmp/dysonfs-remote-keys.txt
-- Applying with an empty listing is refused.

\set ON_ERROR_STOP on

\if :{?default_pool}
\else
\echo 'missing -v default_pool=<pool id marked default = true in config.toml>'
\quit
\endif
\if :{?apply}
\else
\set apply no
\endif

SELECT set_config('dyson.default_pool', :'default_pool', false) AS default_pool,
       set_config('dyson.apply', :'apply', false) AS apply_mode
\gset

BEGIN;

DO $check$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM file_pools
                  WHERE id = current_setting('dyson.default_pool') AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'pool % not found (or soft-deleted); pass the id from config.toml',
      current_setting('dyson.default_pool');
  END IF;
END
$check$;

-- Buckets the rclone scripts renamed: there a bare key is stale and an uploads/
-- key is right. Every other bucket was never touched, so its bare keys are
-- right and only a migration-added prefix is wrong.
CREATE TEMP TABLE renamed_pool ON COMMIT DROP AS
SELECT id, storage_config->>'bucket' AS bucket
  FROM file_pools
 WHERE deleted_at IS NULL
   AND storage_config->>'bucket' IN ('solar-network', 'solar-network-cache');

CREATE TEMP TABLE remote_keys (key text) ON COMMIT DROP;
\copy remote_keys FROM '/tmp/dysonfs-remote-keys.txt'

-- Records that read from the config default pool and nothing else. Empty when
-- that pool is one of the renamed buckets, because then the migration's guess
-- matched the server and nothing was mis-keyed.
CREATE TEMP TABLE fallback_rows ON COMMIT DROP AS
SELECT cf.id AS file_id, cf.object_id, cf.storage_key AS file_key
  FROM cloud_files cf
 WHERE cf.deleted_at IS NULL
   AND cf.pool_id IS NULL
   AND coalesce(cf.storage_key, '') LIKE 'uploads/%'
   AND current_setting('dyson.default_pool')::text NOT IN (SELECT id FROM renamed_pool);

-- ---------------------------------------------------------------------------
-- Preview
-- ---------------------------------------------------------------------------
SELECT 'remote listing keys loaded' AS finding, count(*) AS rows FROM remote_keys
UNION ALL
SELECT 'records reading from the default pool with an uploads/ key', count(*) FROM fallback_rows
UNION ALL
SELECT '  ... safe to revert (bare key present, uploads/ key absent)',
       count(*) FROM fallback_rows f
 WHERE EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(f.file_key, '^(uploads/)+', ''))
   AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = f.file_key)
UNION ALL
SELECT '  ... left alone (neither form present remotely)',
       count(*) FROM fallback_rows f
 WHERE NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(f.file_key, '^(uploads/)+', ''))
   AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = f.file_key)
UNION ALL
SELECT '  ... left alone (both forms present, ambiguous)',
       count(*) FROM fallback_rows f
 WHERE EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(f.file_key, '^(uploads/)+', ''))
   AND EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = f.file_key)
UNION ALL
SELECT 'keys prefixed twice, anywhere', count(*)
  FROM cloud_files WHERE deleted_at IS NULL AND storage_key LIKE 'uploads/uploads/%'
UNION ALL
SELECT 'bare keys in a renamed bucket (missed prefix)', count(*)
  FROM cloud_files cf
 WHERE cf.deleted_at IS NULL
   AND coalesce(cf.storage_key, '') <> ''
   AND cf.storage_key NOT LIKE 'uploads/%'
   AND cf.storage_key NOT LIKE 'media-cache/%'
   AND nullif(cf.pool_id, '') IN (SELECT id FROM renamed_pool)
UNION ALL
SELECT 'rows to revert (detailed below)', count(*) FROM fallback_rows f
 WHERE EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(f.file_key, '^(uploads/)+', ''))
   AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = f.file_key);

SELECT f.file_id, f.file_key AS current_key,
       regexp_replace(f.file_key, '^(uploads/)+', '') AS would_become
  FROM fallback_rows f
 WHERE EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(f.file_key, '^(uploads/)+', ''))
   AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = f.file_key)
 ORDER BY f.file_id
 LIMIT 20;

-- ---------------------------------------------------------------------------
-- Repair
-- ---------------------------------------------------------------------------
DO $repair$
DECLARE
  touched integer;
  total   integer := 0;
BEGIN
  IF current_setting('dyson.apply') <> 'yes' THEN
    RAISE NOTICE 'preview only: nothing written (re-run with -v apply=yes)';
    RETURN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM remote_keys) THEN
    RAISE EXCEPTION 'refusing to apply with an empty bucket listing; populate /tmp/dysonfs-remote-keys.txt first';
  END IF;

  -- 1. Collapse repeated prefixes: nothing writes uploads/uploads/, so such a
  --    key always names an object that never existed.
  UPDATE cloud_files
     SET storage_key = regexp_replace(storage_key, '^(uploads/)+', 'uploads/')
   WHERE deleted_at IS NULL AND storage_key LIKE 'uploads/uploads/%';
  GET DIAGNOSTICS touched = ROW_COUNT; total := total + touched;

  UPDATE file_objects
     SET storage_key = regexp_replace(storage_key, '^(uploads/)+', 'uploads/')
   WHERE storage_key LIKE 'uploads/uploads/%';
  GET DIAGNOSTICS touched = ROW_COUNT; total := total + touched;

  -- 2. Revert the prefix added to fallback rows, only where the object is
  --    demonstrably at the bare key. Repeated prefixes collapse in one step.
  UPDATE cloud_files cf
     SET storage_key = regexp_replace(f.file_key, '^(uploads/)+', '')
    FROM fallback_rows f
   WHERE cf.id = f.file_id
     AND EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(f.file_key, '^(uploads/)+', ''))
     AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = f.file_key);
  GET DIAGNOSTICS touched = ROW_COUNT; total := total + touched;

  -- Their objects too, unless a record with an explicit pool also points at
  -- them: that record's key was legitimately namespaced. Section 1 already
  -- collapsed these, so the source here is the table, not fallback_rows.
  UPDATE file_objects fo
     SET storage_key = regexp_replace(fo.storage_key, '^(uploads/)+', '')
    FROM fallback_rows f
   WHERE fo.id = f.object_id
     AND coalesce(fo.storage_key, '') LIKE 'uploads/%'
     AND EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = regexp_replace(fo.storage_key, '^(uploads/)+', ''))
     AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = fo.storage_key)
     AND NOT EXISTS (
           SELECT 1 FROM cloud_files other
            WHERE other.object_id = fo.id
              AND other.deleted_at IS NULL
              AND nullif(other.pool_id, '') IS NOT NULL);
  GET DIAGNOSTICS touched = ROW_COUNT; total := total + touched;

  -- 3. Prefix bare keys of records in a renamed bucket: the object is under
  --    uploads/ there now, so the bare key points nowhere.
  UPDATE cloud_files cf
     SET storage_key = 'uploads/' || cf.storage_key
   WHERE cf.deleted_at IS NULL
     AND coalesce(cf.storage_key, '') <> ''
     AND cf.storage_key NOT LIKE 'uploads/%'
     AND cf.storage_key NOT LIKE 'media-cache/%'
     AND nullif(cf.pool_id, '') IN (SELECT id FROM renamed_pool)
     AND EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = 'uploads/' || cf.storage_key)
     AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = cf.storage_key);
  GET DIAGNOSTICS touched = ROW_COUNT; total := total + touched;

  -- The objects those records point at must move with them, or the two columns
  -- name different keys.
  UPDATE file_objects fo
     SET storage_key = 'uploads/' || fo.storage_key
   WHERE coalesce(fo.storage_key, '') <> ''
     AND fo.storage_key NOT LIKE 'uploads/%'
     AND fo.storage_key NOT LIKE 'media-cache/%'
     AND EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = 'uploads/' || fo.storage_key)
     AND NOT EXISTS (SELECT 1 FROM remote_keys rk WHERE rk.key = fo.storage_key)
     AND EXISTS (
           SELECT 1 FROM cloud_files cf
            WHERE cf.object_id = fo.id
              AND cf.deleted_at IS NULL
              AND nullif(cf.pool_id, '') IN (SELECT id FROM renamed_pool));
  GET DIAGNOSTICS touched = ROW_COUNT; total := total + touched;

  RAISE NOTICE 'keys repaired: %', total;
END
$repair$;

COMMIT;

-- ---------------------------------------------------------------------------
-- After COMMIT: remaining suspicious rows, read-only. Expect 0 everywhere.
-- When the default pool owns a renamed bucket, records with NULL pool_id
-- read from that bucket and their uploads/ keys are correct, so they
-- are reported as expected-correct instead of as a problem.
-- ---------------------------------------------------------------------------
SELECT 'uploads/uploads/ left' AS finding, count(*) AS rows
  FROM cloud_files WHERE deleted_at IS NULL AND storage_key LIKE 'uploads/uploads/%'
UNION ALL
SELECT 'records reading from the default pool with an uploads/ key',
       count(*) FROM cloud_files
 WHERE deleted_at IS NULL AND pool_id IS NULL
   AND coalesce(storage_key, '') LIKE 'uploads/%'
   AND current_setting('dyson.default_pool') NOT IN (
         SELECT id FROM file_pools
          WHERE deleted_at IS NULL
            AND storage_config->>'bucket' IN ('solar-network', 'solar-network-cache'))
UNION ALL
SELECT 'records reading from a renamed bucket with an uploads/ key (expected: all of them)',
       count(*) FROM cloud_files
 WHERE deleted_at IS NULL AND pool_id IS NULL
   AND coalesce(storage_key, '') LIKE 'uploads/%'
   AND current_setting('dyson.default_pool') IN (
         SELECT id FROM file_pools
          WHERE deleted_at IS NULL
            AND storage_config->>'bucket' IN ('solar-network', 'solar-network-cache'));
