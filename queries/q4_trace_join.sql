-- Q4. trace_id でサービスをまたいで追跡する
SELECT
    t1.trace_id,
    t1.route,
    t1.duration_ms   AS tier1_ms,
    t2.duration_ms   AS tier2_ms,
    t2.db_duration_ms
FROM read_ndjson_auto('logs/tier1.ndjson') t1
JOIN read_ndjson_auto('logs/tier2.ndjson') t2
  ON t1.trace_id = t2.trace_id
ORDER BY t1.time DESC
LIMIT 20;
