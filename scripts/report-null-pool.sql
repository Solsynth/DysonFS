-- Why pool_id / storage_key are NULL, and whether it matters (read-only).
--
-- Neither is required for a read to work:
--   bucket  = pool_id -> else the config default pool   (BackendForFile)
--   key     = file.storage_key -> object.storage_key -> object_id  (ResolveStorageKey)
-- So a row with NULL pool_id and NULL keys still resolves, provided the object
-- really sits at <object_id> in that bucket.
\if :{?default_pool}
\else
\set default_pool ''
\endif
SELECT CASE
         WHEN cf.is_folder                  THEN 'folder (no pool/key by design)'
         WHEN cf.object_id IS NULL          THEN 'no object (pending/never completed)'
         WHEN coalesce(cf.storage_key,'') = '' AND coalesce(fo.storage_key,'') = ''
                                            THEN 'object, key from object_id fallback'
         WHEN coalesce(cf.storage_key,'') <> '' THEN 'file, has its own key'
         ELSE                               'file, key from the object'
       END AS shape,
       CASE WHEN cf.created_at < '2026-05-17'
            THEN 'before pool_id was recorded'
            ELSE 'on/after 2026-05-17' END AS era,
       count(*) AS rows
  FROM cloud_files cf
  LEFT JOIN file_objects fo ON fo.id = cf.object_id
 WHERE cf.pool_id IS NULL
 GROUP BY 1, 2
 ORDER BY 1, 2;
