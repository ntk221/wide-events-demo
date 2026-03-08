-- Q3. DB が詰まった時間帯を特定する
SELECT
    time_bucket(INTERVAL '1 minute', time::TIMESTAMP) AS bucket,
    AVG(db_duration_ms) AS avg_db_ms,
    COUNT(*)            AS count
FROM read_ndjson_auto('logs/tier2.ndjson')
GROUP BY bucket
ORDER BY bucket;
