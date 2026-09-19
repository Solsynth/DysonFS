-- DysonFS key-scheme migration. Run AFTER the rclone bucket scripts:
--   1. scripts/prefix-legacy-keys.sh   (bucket: legacy keys -> uploads/)
--   2. scripts/move-cache-uploads.sh   (bucket: cache uploads/ -> main uploads/)
--
-- Run:  psql "$DATABASE_DSN" -f scripts/migrate-keys.sql
--
-- The two pools involved are resolved from file_pools.storage_config by bucket
-- name, using the same names the rclone scripts take, so a typo aborts the
-- migration instead of matching no rows:
--   -v main_bucket=<bucket>    bucket holding user files  (default solar-network)
--   -v cache_bucket=<bucket>   media transform cache      (default solar-network-cache)
-- Pass -v default_pool=<pool id> / -v cache_pool=<pool id> to skip the lookup.
--
-- Only records stored through those two pools are re-keyed: objects in any
-- other pool's bucket were not touched by the rclone scripts and keep their
-- keys. The migration is a single DO block, i.e. one transaction: if any step
-- fails, nothing is committed.

\set ON_ERROR_STOP on

-- -v values must win over these defaults, and a bare \set would overwrite them.
\if :{?main_bucket}
\else
\set main_bucket solar-network
\endif
\if :{?cache_bucket}
\else
\set cache_bucket solar-network-cache
\endif
\if :{?default_pool}
\else
\set default_pool ''
\endif
\if :{?cache_pool}
\else
\set cache_pool ''
\endif

-- psql does not interpolate variables inside dollar-quoted bodies, so hand the
-- parameters over as session settings; \gset keeps the result out of the log.
SELECT set_config('dyson.main_bucket', :'main_bucket', false) AS main_bucket_set,
       set_config('dyson.cache_bucket', :'cache_bucket', false) AS cache_bucket_set,
       set_config('dyson.default_pool', :'default_pool', false) AS default_pool_set,
       set_config('dyson.cache_pool', :'cache_pool', false) AS cache_pool_set
\gset

DO $migration$
DECLARE
  main_bucket  text := current_setting('dyson.main_bucket');
  cache_bucket text := current_setting('dyson.cache_bucket');
  main_pool    text := nullif(current_setting('dyson.default_pool'), '');
  cache_pool   text := nullif(current_setting('dyson.cache_pool'), '');
  scope        text[];
  matches      integer;
  touched      integer;
  keyed        integer := 0;
  repointed    integer;
  unresolved   bigint;
BEGIN
  -- -------------------------------------------------------------------------
  -- Resolve the pools that own the two buckets before touching any row.
  -- -------------------------------------------------------------------------
  IF main_pool IS NULL THEN
    SELECT count(*), coalesce(min(id), '') INTO matches, main_pool
      FROM file_pools
     WHERE deleted_at IS NULL AND storage_config->>'bucket' = main_bucket;
    IF matches <> 1 THEN
      RAISE EXCEPTION 'main bucket % must belong to exactly one pool, found %; pass -v default_pool=<pool id>', main_bucket, matches;
    END IF;
    IF EXISTS (SELECT 1 FROM file_pools WHERE id = main_pool AND is_hidden) THEN
      RAISE EXCEPTION 'pool % owns main bucket % but is hidden (internal-only); pass -v main_bucket=<bucket> or -v default_pool=<pool id>', main_pool, main_bucket;
    END IF;
  ELSIF NOT EXISTS (SELECT 1 FROM file_pools WHERE id = main_pool AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'pool % given as default_pool does not exist', main_pool;
  END IF;

  IF cache_pool IS NULL THEN
    SELECT count(*), coalesce(min(id), '') INTO matches, cache_pool
      FROM file_pools
     WHERE deleted_at IS NULL AND storage_config->>'bucket' = cache_bucket;
    IF matches <> 1 THEN
      RAISE EXCEPTION 'cache bucket % must belong to exactly one pool, found %; pass -v cache_pool=<pool id>', cache_bucket, matches;
    END IF;
    -- Repointing rows out of a visible pool would strand their objects, so a
    -- bucket-derived cache pool must be an internal one (see is_hidden).
    IF NOT EXISTS (SELECT 1 FROM file_pools WHERE id = cache_pool AND is_hidden) THEN
      RAISE EXCEPTION 'pool % owns cache bucket % but is not hidden (internal-only); pass -v cache_pool=<pool id> if that is intended', cache_pool, cache_bucket;
    END IF;
  ELSIF NOT EXISTS (SELECT 1 FROM file_pools WHERE id = cache_pool AND deleted_at IS NULL) THEN
    RAISE EXCEPTION 'pool % given as cache_pool does not exist', cache_pool;
  END IF;

  IF main_pool = cache_pool THEN
    RAISE EXCEPTION 'main bucket % and cache bucket % resolve to the same pool %; check -v main_bucket/-v cache_bucket', main_bucket, cache_bucket, main_pool;
  END IF;

  -- A record's bucket is its pool_id, falling back to the default backend,
  -- exactly like BackendForFile/backendForStorageTarget.
  scope := array[main_pool, cache_pool];

  -- -------------------------------------------------------------------------
  -- A. Prefix legacy (unprefixed) storage keys with uploads/
  -- -------------------------------------------------------------------------
  -- Mirrors prefix-legacy-keys.sh. file_objects and cloud_files name the same
  -- object, so both columns must move together; media-cache/ objects stay in
  -- the cache bucket and keep their keys.
  UPDATE file_objects fo
     SET storage_key = 'uploads/' || fo.storage_key
   WHERE coalesce(fo.storage_key, '') <> ''
     AND fo.storage_key NOT LIKE 'uploads/%'
     AND fo.storage_key NOT LIKE 'media-cache/%'
     AND EXISTS (
           SELECT 1 FROM cloud_files cf
            WHERE cf.object_id = fo.id
              AND coalesce(nullif(cf.pool_id, ''), main_pool) = ANY (scope)
         );
  GET DIAGNOSTICS touched = ROW_COUNT;
  keyed := keyed + touched;

  UPDATE cloud_files
     SET storage_key = 'uploads/' || storage_key
   WHERE coalesce(storage_key, '') <> ''
     AND storage_key NOT LIKE 'uploads/%'
     AND storage_key NOT LIKE 'media-cache/%'
     AND coalesce(nullif(pool_id, ''), main_pool) = ANY (scope);
  GET DIAGNOSTICS touched = ROW_COUNT;
  keyed := keyed + touched;

  -- Damage from the first revision of this script, which prefixed keys without
  -- checking the bucket: re-running it prefixed already-prefixed keys. The
  -- object moved once, so the repeats collapse. Records in other pools are left
  -- to the census below, since their buckets never moved either way.
  UPDATE file_objects fo
     SET storage_key = regexp_replace(fo.storage_key, '^(uploads/)+', 'uploads/')
   WHERE fo.storage_key LIKE 'uploads/uploads/%'
     AND EXISTS (
           SELECT 1 FROM cloud_files cf
            WHERE cf.object_id = fo.id
              AND coalesce(nullif(cf.pool_id, ''), main_pool) = ANY (scope)
         );
  GET DIAGNOSTICS touched = ROW_COUNT;
  keyed := keyed + touched;

  UPDATE cloud_files
     SET storage_key = regexp_replace(storage_key, '^(uploads/)+', 'uploads/')
   WHERE storage_key LIKE 'uploads/uploads/%'
     AND coalesce(nullif(pool_id, ''), main_pool) = ANY (scope);
  GET DIAGNOSTICS touched = ROW_COUNT;
  keyed := keyed + touched;

  -- -------------------------------------------------------------------------
  -- B. Rebuild keys that cannot name their object
  -- -------------------------------------------------------------------------
  -- An empty key resolves to the bare object id at read time (see
  -- ResolveStorageKey), which the bucket rename left behind; a lone 'uploads/'
  -- is the same defect left by a partial run of an earlier revision. Both are
  -- rebuilt from the object's key, or from the object id when that is unusable.
  UPDATE cloud_files cf
     SET storage_key = coalesce(
           (SELECT fo.storage_key
              FROM file_objects fo
             WHERE fo.id = cf.object_id
               AND coalesce(fo.storage_key, '') NOT IN ('', 'uploads/')),
           'uploads/' || cf.object_id)
   WHERE coalesce(cf.storage_key, '') IN ('', 'uploads/')
     AND coalesce(cf.object_id, '') <> ''
     AND coalesce(nullif(cf.pool_id, ''), main_pool) = ANY (scope);
  GET DIAGNOSTICS touched = ROW_COUNT;
  keyed := keyed + touched;

  UPDATE file_objects fo
     SET storage_key = 'uploads/' || fo.id
   WHERE coalesce(fo.storage_key, '') IN ('', 'uploads/')
     AND EXISTS (
           SELECT 1 FROM cloud_files cf
            WHERE cf.object_id = fo.id
              AND coalesce(nullif(cf.pool_id, ''), main_pool) = ANY (scope)
         );
  GET DIAGNOSTICS touched = ROW_COUNT;
  keyed := keyed + touched;

  -- -------------------------------------------------------------------------
  -- C. Repoint records that were misrouted into the cache pool
  -- -------------------------------------------------------------------------
  -- move-cache-uploads.sh moved their objects into the main bucket, so
  -- pool_id must name the main pool or openFile still resolves the
  -- cache bucket and 404s. media-cache/ rows stay where they are.
  UPDATE cloud_files
     SET pool_id = main_pool
   WHERE pool_id = cache_pool
     AND coalesce(storage_key, '') NOT LIKE 'media-cache/%';
  GET DIAGNOSTICS repointed = ROW_COUNT;

  -- -------------------------------------------------------------------------
  -- Contract: after the bucket rename, every key of a record stored through
  -- the main or cache bucket must resolve under uploads/ (media-cache/ stays
  -- in the cache bucket). Fail the whole migration rather than commit keys the
  -- server would 404 on.
  -- -------------------------------------------------------------------------
  SELECT count(*) INTO unresolved
    FROM (
      SELECT coalesce(nullif(cf.storage_key, ''), nullif(fo.storage_key, ''), cf.object_id, '') AS key
        FROM cloud_files cf
        LEFT JOIN file_objects fo ON fo.id = cf.object_id
       WHERE coalesce(nullif(cf.pool_id, ''), main_pool) = ANY (scope)
      UNION ALL
      SELECT coalesce(fo.storage_key, '')
        FROM file_objects fo
       WHERE EXISTS (
             SELECT 1 FROM cloud_files cf
              WHERE cf.object_id = fo.id
                AND coalesce(nullif(cf.pool_id, ''), main_pool) = ANY (scope)
           )
    ) keys
   WHERE key <> ''
     AND key NOT LIKE 'uploads/%'
     AND key NOT LIKE 'media-cache/%';
  IF unresolved > 0 THEN
    RAISE EXCEPTION '% keys still resolve outside uploads/; the bucket rename looks incomplete, so nothing was committed', unresolved;
  END IF;

  RAISE NOTICE 'migrated: % object keys prefixed or filled in, % cloud_files rows repointed from pool % to pool %', keyed, repointed, cache_pool, main_pool;

  -- Hand the resolved ids to the read-only census below.
  PERFORM set_config('dyson.resolved_main_pool', main_pool, false);
  PERFORM set_config('dyson.resolved_cache_pool', cache_pool, false);
END
$migration$;

-- ---------------------------------------------------------------------------
-- Census (read-only). Every count is expected to be 0. A non-zero count means
-- rows this migration deliberately left alone, and each needs a decision.
-- ---------------------------------------------------------------------------
SELECT 'in-scope rows whose key cannot name their object' AS problem,
       count(*) AS rows
  FROM cloud_files cf
 WHERE coalesce(nullif(cf.pool_id, ''), current_setting('dyson.resolved_main_pool'))
       IN (current_setting('dyson.resolved_main_pool'), current_setting('dyson.resolved_cache_pool'))
   AND coalesce(cf.object_id, '') <> ''
   AND coalesce(cf.storage_key, '') IN ('', 'uploads/')
UNION ALL
-- An earlier revision of this script prefixed keys without checking which
-- bucket a record belongs to; such rows point at an object that never moved.
SELECT 'keys prefixed twice', count(*)
  FROM file_objects
 WHERE storage_key LIKE 'uploads/uploads/%'
UNION ALL
SELECT 'rows outside the migrated buckets carrying an uploads/ key',
       count(*)
  FROM cloud_files cf
 WHERE coalesce(nullif(cf.pool_id, ''), current_setting('dyson.resolved_main_pool'))
       NOT IN (current_setting('dyson.resolved_main_pool'), current_setting('dyson.resolved_cache_pool'))
   AND coalesce(cf.storage_key, '') LIKE 'uploads/%'
UNION ALL
-- In-flight direct uploads (presigned parts) are not part of this migration:
-- completing one after the bucket rename stats a key that no longer exists.
SELECT 'unfinished upload tasks keyed outside uploads/', count(*)
  FROM persistent_tasks
 WHERE deleted_at IS NULL
   AND coalesce(source_key, '') <> ''
   AND source_key NOT LIKE 'uploads/%'
   AND source_key NOT LIKE 'media-cache/%'
ORDER BY problem;
