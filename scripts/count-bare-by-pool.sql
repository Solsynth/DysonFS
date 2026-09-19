-- Where do the not-prefixed keys read from, and which are actually broken?
--
--   psql "$DATABASE_DSN" -v default_pool=<pool marked default = true> -f count-bare-by-pool.sql
--
-- Reads use pool_id ONLY: BackendForFile (service.go:276) falls back to the
-- config default pool when pool_id is NULL, so that decides the bucket a key
-- must exist in.
--
-- Watch the predicate: `pool_id != '<id>'` silently drops NULL pool_id rows,
-- because NULL != x is NULL, not true. Use IS DISTINCT FROM.
\if :{?default_pool}
\else
\set default_pool ''
\endif

CREATE OR REPLACE TEMP VIEW cf AS
SELECT cf.id, cf.object_id, cf.pool_id,
       coalesce(nullif(cf.storage_key,''), nullif(fo.storage_key,''), cf.object_id, '') AS key,
       coalesce(nullif(cf.pool_id,''), :'default_pool') AS read_pool
  FROM cloud_files cf
  LEFT JOIN file_objects fo ON fo.id = cf.object_id
 WHERE cf.deleted_at IS NULL;

-- 1. The predicate trap, on cloud_files.
SELECT 'pool_id IS NULL (dropped by pool_id != :id)' AS measure, count(*) AS rows FROM cf WHERE pool_id IS NULL
UNION ALL SELECT 'pool_id IS DISTINCT FROM the main pool', count(*) FROM cf WHERE pool_id IS DISTINCT FROM 'c53136a6-9152-4ecb-9f88-43c41438c23e';

-- 2. Not-prefixed keys by the bucket they READ from.
SELECT coalesce(p.name, '<missing pool row>') AS read_from,
       coalesce(p.storage_config->>'bucket', '-') AS bucket,
       CASE WHEN p.id IS NULL THEN 'UNKNOWN: pass -v default_pool=<id>'
            WHEN p.storage_config->>'bucket' IN ('solar-network','solar-network-cache')
                 THEN 'BROKEN: bare key in a renamed bucket'
            ELSE 'expected: bucket never renamed' END AS verdict,
       count(*) AS file_rows
  FROM cf
  LEFT JOIN file_pools p ON p.id = cf.read_pool
 WHERE cf.key <> ''
   AND cf.key NOT LIKE 'uploads/%'
   AND cf.key NOT LIKE 'media-cache/%'
 GROUP BY 1, 2, 3
 ORDER BY 3, 1;

-- 3. The headline number, no grouping.
SELECT count(*) AS not_prefixed_rows FROM cf
 WHERE key <> '' AND key NOT LIKE 'uploads/%' AND key NOT LIKE 'media-cache/%';
