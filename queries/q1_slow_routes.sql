-- Q1. どのルートが遅い？
SELECT
    route,
    COUNT(*)           AS count,
    AVG(duration_ms)   AS avg_ms,
    MAX(duration_ms)   AS max_ms
FROM read_ndjson_auto('logs/tier1.ndjson')
GROUP BY route
ORDER BY avg_ms DESC;
