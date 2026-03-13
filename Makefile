.PHONY: up down logs logs-reset query q1 q2 q3 q4 traffic saas-down saas-up

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	@mkdir -p logs
	@docker compose logs tier1 --no-log-prefix 2>/dev/null >> logs/tier1.ndjson && sort -u -o logs/tier1.ndjson logs/tier1.ndjson
	@docker compose logs tier2 --no-log-prefix 2>/dev/null >> logs/tier2.ndjson && sort -u -o logs/tier2.ndjson logs/tier2.ndjson
	@docker compose logs cons  --no-log-prefix 2>/dev/null >> logs/cons.ndjson  && sort -u -o logs/cons.ndjson  logs/cons.ndjson
	@echo "Collected logs -> logs/tier1.ndjson, logs/tier2.ndjson, logs/cons.ndjson"

logs-reset:
	@rm -f logs/*.ndjson
	@echo "Logs cleared."

traffic:
	@echo "Generating traffic..."
	@for i in $$(seq 1 20); do \
		curl -s localhost:8100/fast   > /dev/null & \
		curl -s localhost:8100/slow   > /dev/null & \
		curl -s localhost:8100/random > /dev/null & \
		curl -s localhost:8100/error  > /dev/null & \
	done; wait
	@echo "Done. Run 'make logs' to collect, then 'make q1' etc. to query."

query:
	@test -n "$(Q)" || (echo "Usage: make query Q=queries/q1_slow_routes.sql" && exit 1)
	@make logs --no-print-directory
	@echo "---"; cat $(Q); echo "---"
	@duckdb < $(Q)

q1: logs ## どのルートが遅い？
	@echo "---"; cat queries/q1_slow_routes.sql; echo "---"
	@duckdb < queries/q1_slow_routes.sql

q2: logs ## SaaS 障害と遅延の相関
	@echo "---"; cat queries/q2_saas_correlation.sql; echo "---"
	@duckdb < queries/q2_saas_correlation.sql

q3: logs ## DB ボトルネックの時間帯
	@echo "---"; cat queries/q3_db_bottleneck.sql; echo "---"
	@duckdb < queries/q3_db_bottleneck.sql

q4: logs ## trace_id でサービス横断追跡
	@echo "---"; cat queries/q4_trace_join.sql; echo "---"
	@duckdb < queries/q4_trace_join.sql

saas-down: ## SaaS 障害 (タイムアウト → レイテンシ悔化)
	@curl -s http://localhost:8700/admin/down

saas-up: ## SaaS 復旧
	@curl -s http://localhost:8700/admin/up

help: ## コマンド一覧
	@echo "使い方:"
	@echo "  make up         サービス起動"
	@echo "  make traffic    テストトラフィック生成"
	@echo "  make logs       ログ収集（追記・重複排除）"
	@echo "  make logs-reset ログをクリア"
	@echo "  make q1         どのルートが遅い？"
	@echo "  make q2         SaaS 障害と遅延の相関"
	@echo "  make q3         DB ボトルネックの時間帯"
	@echo "  make q4         trace_id でサービス横断追跡"
	@echo "  make saas-down  SaaS 障害 (タイムアウト)"
	@echo "  make saas-up    SaaS 復旧"
	@echo "  make down       サービス停止"
