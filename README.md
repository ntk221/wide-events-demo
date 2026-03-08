# wide-events-demo

[gkspranger/o11y-demo](https://github.com/gkspranger/o11y-demo) のアーキテクチャをベースに、OTel なしで **Wide Events** を体験するデモ。Go の `log/slog` で NDJSON を吐き、DuckDB で集計する。

## Wide Events とは

1リクエストの処理全体を **1行の JSON** に集約するログ戦略（Canonical Log Lines）。

```json
{"service":"tier1","trace_id":"abc-123","route":"/random","was_slow":true,"duration_ms":1823,"queue_ok":true,"saas_ok":true,"saas_status":200,"tier2_ok":true,"tier2_duration_ms":95}
```

## アーキテクチャ

```
[browser] → [waf] → [web] → [tier1] → [saas]  (mock)
                                     → [queue] → [cons] → [db]
                                     → [tier2] → [db]
```

| サービス | ポート | 説明 |
|---------|--------|------|
| waf     | 8100   | HAProxy — エントリポイント |
| web     | 8200   | nginx リバースプロキシ |
| tier1   | 8300   | Go HTTP サーバー |
| tier2   | 8400   | Go gRPC サーバー |
| db      | 8500   | MySQL |
| queue   | 8600   | RabbitMQ Management UI |
| saas    | 8700   | Mock SaaS（障害シミュレーション用） |

tier1 → tier2 は gRPC。Wide Event の収集は `grpc.UnaryInterceptor` で行う。
tier1 のブラウザ向けは HTTP middleware、パターンは同じ: `eventAttrs` を context に注入 → 各層がフィールドを追加 → 最後に1行出力。

## 前提条件

- Docker / Docker Compose
- [DuckDB](https://duckdb.org/) CLI

## クイックスタート

```bash
make up            # 全サービス起動
make traffic       # テストトラフィック生成
make q1            # どのルートが遅い？
```

## コマンド一覧

```bash
make up            # サービス起動
make down          # サービス停止
make traffic       # テストトラフィック生成（20リクエスト × 4ルート）
make logs          # ログ収集（追記・重複排除）
make logs-reset    # ログをクリア
make q1            # どのルートが遅い？
make q2            # SaaS 障害と遅延の相関
make q3            # DB ボトルネックの時間帯
make q4            # trace_id でサービス横断追跡
make saas-down     # SaaS 障害シミュレート（タイムアウト）
make saas-up       # SaaS 復旧
make help          # コマンド一覧
```

## 障害シミュレーション

`saas` サービスは Admin API を持っており、ファイル編集やコンテナ再起動なしで障害を切り替えられる。

```bash
# 1. 正常時のトラフィックを流す
make traffic

# 2. SaaS を落とす（リクエストが 5 秒タイムアウトするようになる）
make saas-down

# 3. 障害中のトラフィックを流す
make traffic

# 4. SaaS を復旧
make saas-up

# 5. 相関を確認
make q2
```

結果の例:

```
┌─────────┬───────┬────────┐
│ saas_ok │ count │ avg_ms │
├─────────┼───────┼────────┤
│ true    │    10 │  154.0 │
│ false   │     5 │ 5003.0 │
└─────────┴───────┴────────┘
```

SaaS 障害時はタイムアウト待ちが発生し、レイテンシが **30 倍以上** に悪化する。Wide Events で `saas_ok` と `duration_ms` が同じ行に入っているからこそ、SQL 一発で相関が見える。

## クレジット

アーキテクチャと設定ファイルは [gkspranger/o11y-demo](https://github.com/gkspranger/o11y-demo) から流用。
