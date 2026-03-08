# wide-events-demo

[gkspranger/o11y-demo](https://github.com/gkspranger/o11y-demo) のアーキテクチャをベースに、OTel なしで **Wide Events** を体験するデモ。Go の `log/slog` で NDJSON を吐き、DuckDB で集計する。

## Wide Events とは

1リクエストの処理全体を **1行の JSON** に集約するログ戦略（Canonical Log Lines）。

```json
{"service":"tier1","trace_id":"abc-123","route":"/random","was_slow":true,"duration_ms":1823,"queue_ok":true,"saas_ok":true,"saas_status":200,"tier2_ok":true,"tier2_duration_ms":95}
```

## アーキテクチャ

```
[browser] → [waf] → [web] → [tier1] → [saas]  (外部)
                                     → [queue] → [cons] → [db]
                                     → [tier2] → [db]
```

tier1 → tier2 は gRPC。Wide Event の収集は `grpc.UnaryInterceptor` で行う。
tier1 のブラウザ向けは HTTP middleware、パターンは同じ: `eventAttrs` を context に注入 → 各層がフィールドを追加 → 最後に1行出力。

## 使い方

```bash
make up                                # docker compose up
make logs                              # NDJSON 収集
curl localhost:8100/{fast,slow,random,error}
duckdb < queries/q1_slow_routes.sql    # どのルートが遅い？
duckdb < queries/q4_trace_join.sql     # trace_id でサービス横断
```

## クレジット

アーキテクチャと設定ファイルは [gkspranger/o11y-demo](https://github.com/gkspranger/o11y-demo) から流用。
