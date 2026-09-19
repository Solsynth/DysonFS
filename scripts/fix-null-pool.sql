-- DysonFS repair: attribute and re-key records with no pool_id.
--
-- These rows predate pool attribution: pool_id is NULL/'' because the pool was
-- never recorded (the legacy uploader stored objects under a bare key and had
-- no pool concept). With no pool_id, BackendForFile (internal/service
-- service.go:276) reads them from the default pool, but their bare key does not
-- match the uploads/-prefixed object the path change left in the bucket.
--
-- For every live, non-folder cloud_files row with NULLIF(pool_id,'') IS NULL:
--   1. Attribution: pool_id = the default pool (config.toml, default = true).
--   2. Keys: the bare storage_key (cloud_files and the referenced file_objects)
--      is prefixed with uploads/, the scheme in effect since the path change.
--
-- Assumption: the objects of these rows physically sit at uploads/<key> in the
-- default pool's bucket. The preview lists every key that will change;
-- spot-check a sample against the bucket before applying. The prefix guards
-- make re-runs a no-op.
--
-- Single file, one explicit transaction (BEGIN ... COMMIT): the temp scope
-- table survives to the preview, and apply commits atomically or not at all.
-- Without apply=yes the COMMIT is a no-op; the transaction rolls back on error
-- (ON_ERROR_STOP).
--
-- Preview:  psql "$DATABASE_DSN" -v default_pool=<id> -f scripts/fix-null-pool.sql
-- Apply:    psql "$DATABASE_DSN" -v default_pool=<id> -v apply=yes -f scripts/fix-null-pool.sql
--
-- default_pool is REQUIRED: the pool marked default = true in config.toml.

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

DO $check$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM file_pools
                  WHERE id = current_setting('dyson.default_pool') AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'pool % not found (or soft-deleted); pass the id from config.toml',
      current_setting('dyson.default_pool');
  END IF;
END
$check$;

-- One transaction for the whole run: the ON COMMIT DROP temp table would
-- otherwise vanish at the autocommit boundary of its own CREATE statement.
BEGIN;

-- Scope: live, non-folder cloud_files with empty pool_id (NULL or '').
CREATE TEMP TABLE fix_scope ON COMMIT DROP AS
SELECT cf.id, cf.object_id
  FROM cloud_files cf
 WHERE cf.deleted_at IS NULL
   AND cf.is_folder = FALSE
   AND NULLIF(cf.pool_id, '') IS NULL;

-- ---------------------------------------------------------------------------
-- Preview (read-only): scope shape, then the exact key changes.
-- ---------------------------------------------------------------------------
SELECT count(*) AS scope_rows,
       count(*) FILTER (WHERE coalesce(nullif(cf.storage_key,''), fo.storage_key) LIKE 'uploads/%') AS already_prefixed,
       count(*) FILTER (WHERE coalesce(nullif(cf.storage_key,''), fo.storage_key) IS NULL) AS no_key
  FROM fix_scope fs
  JOIN cloud_files cf ON cf.id = fs.id
  LEFT JOIN file_objects fo ON fo.id = cf.object_id;

SELECT 'bare key -> will be prefixed' AS status, count(*) AS rows
  FROM fix_scope fs
  JOIN cloud_files cf ON cf.id = fs.id
  LEFT JOIN file_objects fo ON fo.id = cf.object_id
 WHERE coalesce(nullif(cf.storage_key,''), fo.storage_key) NOT LIKE 'uploads/%'
   AND coalesce(nullif(cf.storage_key,''), fo.storage_key) NOT LIKE 'media-cache/%'
   AND coalesce(nullif(cf.storage_key,''), fo.storage_key) IS NOT NULL
UNION ALL
SELECT 'no key at all (object_id fallback; NOT auto-fixed)', count(*)
  FROM fix_scope fs
  JOIN cloud_files cf ON cf.id = fs.id
  LEFT JOIN file_objects fo ON fo.id = cf.object_id
 WHERE coalesce(nullif(cf.storage_key,''), fo.storage_key) IS NULL;

-- Sample of the rows that will change.
SELECT cf.id,
       cf.pool_id                                              AS pool_before,
       coalesce(nullif(cf.storage_key,''), fo.storage_key)     AS key_before,
       'uploads/' || coalesce(nullif(cf.storage_key,''), fo.storage_key) AS key_after
  FROM fix_scope fs
  JOIN cloud_files cf ON cf.id = fs.id
  LEFT JOIN file_objects fo ON fo.id = cf.object_id
 WHERE coalesce(nullif(cf.storage_key,''), fo.storage_key) NOT LIKE 'uploads/%'
   AND coalesce(nullif(cf.storage_key,''), fo.storage_key) NOT LIKE 'media-cache/%'
   AND coalesce(nullif(cf.storage_key,''), fo.storage_key) IS NOT NULL
 ORDER BY cf.created_at DESC
 LIMIT 200;

-- ---------------------------------------------------------------------------
-- Apply (one transaction; no-op when run without -v apply=yes).
-- ---------------------------------------------------------------------------
\if :apply
DO $fix$
DECLARE
  default_pool  text := current_setting('dyson.default_pool');
  attributed    integer := 0;
  files_keyed   integer := 0;
  objects_keyed integer := 0;
BEGIN
  -- 1. Attribution: every scope row belongs to the default pool.
  UPDATE cloud_files cf
     SET pool_id = default_pool
    FROM fix_scope fs
   WHERE cf.id = fs.id;
  GET DIAGNOSTICS attributed = ROW_COUNT;

  -- 2. Prefix bare file-level keys.
  UPDATE cloud_files cf
     SET storage_key = 'uploads/' || cf.storage_key
    FROM fix_scope fs
   WHERE cf.id = fs.id
     AND NULLIF(cf.storage_key, '') IS NOT NULL
     AND cf.storage_key NOT LIKE 'uploads/%'
     AND cf.storage_key NOT LIKE 'media-cache/%';
  GET DIAGNOSTICS files_keyed = ROW_COUNT;

  -- 3. Prefix bare object-level keys referenced by those rows.
  UPDATE file_objects fo
     SET storage_key = 'uploads/' || fo.storage_key
   WHERE NULLIF(fo.storage_key, '') IS NOT NULL
     AND fo.storage_key NOT LIKE 'uploads/%'
     AND fo.storage_key NOT LIKE 'media-cache/%'
     AND EXISTS (SELECT 1 FROM fix_scope fs WHERE fs.object_id = fo.id);
  GET DIAGNOSTICS objects_keyed = ROW_COUNT;

  RAISE NOTICE 'attributed: %, files keyed: %, objects keyed: %',
    attributed, files_keyed, objects_keyed;
END
$fix$;
\endif

COMMIT;
