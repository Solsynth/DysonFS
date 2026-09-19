-- How many storage keys are not namespaced under uploads/? (read-only)
--
--   psql "$DATABASE_DSN" -v default_pool=<pool marked default = true> -f count-bare.sql
--
-- Soft-deleted rows are included by default (they are read and purged too);
-- add -v live_only=yes to exclude them. default_pool is needed only for the
-- per-pool part, where a record with NULL pool_id resolves there
-- (internal/service BackendForFile).
\if :{?default_pool}
\else
\set default_pool ''
\endif
\if :{?live_only}
\else
\set live_only no
\endif

CREATE OR REPLACE TEMP VIEW fo AS
  SELECT * FROM file_objects WHERE :'live_only' <> 'yes' OR deleted_at IS NULL;
CREATE OR REPLACE TEMP VIEW cf AS
  SELECT * FROM cloud_files WHERE :'live_only' <> 'yes' OR deleted_at IS NULL;

-- 1. State of the column itself.
SELECT 'file_objects' AS source, 'bare (not prefixed)' AS state, count(*) AS rows
  FROM fo WHERE coalesce(storage_key,'') <> ''
              AND storage_key NOT LIKE 'uploads/%' AND storage_key NOT LIKE 'media-cache/%'
UNION ALL SELECT 'file_objects', 'uploads/…', count(*) FROM fo WHERE storage_key LIKE 'uploads/%'
UNION ALL SELECT 'file_objects', 'media-cache/…', count(*) FROM fo WHERE storage_key LIKE 'media-cache/%'
UNION ALL SELECT 'file_objects', 'NULL key', count(*) FROM fo WHERE storage_key IS NULL
UNION ALL SELECT 'file_objects', 'empty key', count(*) FROM fo WHERE storage_key = ''
UNION ALL SELECT 'cloud_files', 'bare (not prefixed)', count(*)
  FROM cf WHERE coalesce(storage_key,'') <> ''
              AND storage_key NOT LIKE 'uploads/%' AND storage_key NOT LIKE 'media-cache/%'
UNION ALL SELECT 'cloud_files', 'uploads/…', count(*) FROM cf WHERE storage_key LIKE 'uploads/%'
UNION ALL SELECT 'cloud_files', 'media-cache/…', count(*) FROM cf WHERE storage_key LIKE 'media-cache/%'
UNION ALL SELECT 'cloud_files', 'NULL key (object may carry one)', count(*) FROM cf WHERE storage_key IS NULL
UNION ALL SELECT 'cloud_files', 'empty key', count(*) FROM cf WHERE storage_key = ''
ORDER BY source DESC, state;

-- 2. Bare keys by the pool they actually read from. Only 'renamed by the
--    rclone scripts' rows are broken; the rest are untouched legacy buckets.
SELECT coalesce(p.name, '<missing pool row>') AS pool_name,
       coalesce(p.storage_config->>'bucket', '-') AS bucket,
       CASE WHEN coalesce(p.storage_config->>'bucket','') IN ('solar-network','solar-network-cache')
            THEN 'renamed by the rclone scripts -> BROKEN'
            ELSE 'never renamed -> expected' END AS verdict,
       count(DISTINCT coalesce(fo2.id, cf.object_id)) AS objects,
       count(*) AS file_rows
  FROM cf
  LEFT JOIN file_objects fo2 ON fo2.id = cf.object_id
  LEFT JOIN file_pools  p   ON p.id = coalesce(nullif(cf.pool_id,''), :'default_pool')
 WHERE coalesce(nullif(cf.storage_key,''), nullif(fo2.storage_key,''), cf.object_id, '') <> ''
   AND coalesce(nullif(cf.storage_key,''), nullif(fo2.storage_key,''), cf.object_id, '') NOT LIKE 'uploads/%'
   AND coalesce(nullif(cf.storage_key,''), nullif(fo2.storage_key,''), cf.object_id, '') NOT LIKE 'media-cache/%'
 GROUP BY 1, 2, 3
 ORDER BY 3, 1;
