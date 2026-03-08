.PHONY: up down logs query

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	@mkdir -p logs
	docker compose logs tier1 --no-log-prefix -f > logs/tier1.ndjson &
	docker compose logs tier2 --no-log-prefix -f > logs/tier2.ndjson &
	docker compose logs cons  --no-log-prefix -f > logs/cons.ndjson  &
	@echo "Logging started. Files: logs/tier1.ndjson, logs/tier2.ndjson, logs/cons.ndjson"

query:
	duckdb < $(Q)
