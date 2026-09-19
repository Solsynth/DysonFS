-- Where do the unprefixed storage keys live, and is that expected? (read-only)
-- A record's backend is its pool_id, else the pool marked
-- `default = true` in config.toml  ->  -v default_pool=<id>
\if :{?default_pool}
\else
\set default_pool ''
\endif
SELECT coalesce(p.name, '<missing pool row>') AS pool_name,
       coalesce(p.storage_config->>'bucket', '-') AS bucket,
       coalesce(p.storage_config->>'endpoint', '-') AS endpoint,
       CASE WHEN coalesce(p.storage_config->>'bucket','') IN ('solar-network','solar-network-cache')
            THEN 'renamed by the rclone scripts'
            ELSE 'never renamed' END AS bucket_renamed,
       CASE WHEN coalesce(p.storage_config->>'bucket','') IN ('solar-network','solar-network-cache')
            THEN 'BROKEN: bare key in a renamed bucket'
            ELSE 'ok: legacy key, object still there' END AS verdict,
       count(*) AS files
  FROM cloud_files cf
  LEFT JOIN file_objects fo ON fo.id = cf.object_id
  LEFT JOIN file_pools  p  ON p.id = coalesce(nullif(cf.pool_id,''), :'default_pool')
 WHERE cf.deleted_at IS NULL
   AND coalesce(nullif(cf.storage_key,''), nullif(fo.storage_key,''), cf.object_id, '') <> ''
   AND coalesce(nullif(cf.storage_key,''), nullif(fo.storage_key,''), cf.object_id, '') NOT LIKE 'uploads/%'
   AND coalesce(nullif(cf.storage_key,''), nullif(fo.storage_key,''), cf.object_id, '') NOT LIKE 'media-cache/%'
 GROUP BY 1, 2, 3, 4, 5
 ORDER BY 5, 1;
