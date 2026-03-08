-- Q2. SaaS 障害と遅延は相関しているか？
SELECT
    saas_ok,
    COUNT(*)         AS count,
    AVG(duration_ms) AS avg_ms
FROM read_ndjson_auto('logs/tier1.ndjson')
GROUP BY saas_ok;
