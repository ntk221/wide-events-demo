# wide-events-demo

「薄いログでは答えられない問いに、Wide Events なら即答できる」を体験するデモ。

[gkspranger/o11y-demo](https://github.com/gkspranger/o11y-demo) のマルチティアアーキテクチャをベースに、OTel も Span も使わず **Go の `log/slog`（標準ライブラリ）だけ** で Wide Events を実装し、DuckDB で集計する。

## Before / After

### Before（従来のログ）

```
TIER1: called fast function
TIER1: calling tier1 function
TIER2: queried DB
```

インシデント時に「なぜ遅い？」「どのパスが詰まっている？」が答えられない。

### After（Wide Events）

1リクエスト = 1行の JSON。処理の全コンテキストが1イベントに集約される。

```json
{
  "time": "2026-03-08T12:00:00Z",
  "service": "tier1",
  "trace_id": "abc-123",
  "route": "/random",
  "was_slow": true,
  "duration_ms": 1823,
  "queue_ok": true,
  "queue_duration_ms": 312,
  "saas_ok": true,
  "saas_status": 200,
  "saas_duration_ms": 201,
  "tier2_ok": true,
  "tier2_duration_ms": 95
}
```

DuckDB で即答：

```sql
SELECT route, AVG(duration_ms), COUNT(*)
FROM read_ndjson_auto('logs/tier1.ndjson')
GROUP BY route ORDER BY AVG(duration_ms) DESC;
```

## アーキテクチャ

```
[browser]
    |
    v
[waf]        haproxy
    |
    v
[web]        nginx
    |
    v
[tier1]      Go HTTP server
    |--- [saas]   githubstatus.com (外部)
    |--- [queue]  RabbitMQ
    '--- [tier2]  Go HTTP server (or gRPC server)
                      |
                      v
                  [db]  MySQL

[cons]       Go  <-- queue --> [db]
```

waf / web / queue / db はベンダー扱い（[o11y-demo](https://github.com/gkspranger/o11y-demo) の構成を踏襲）。
Go で実装するのは **tier1 / tier2 / cons** の3サービス。

## 2つの実装

| ディレクトリ | tier1 → tier2 通信 | Wide Event のフック |
|---|---|---|
| `tier1/` `tier2/` `cons/` | HTTP | `http.Handler` middleware |
| `grpc/tier1/` `grpc/tier2/` `grpc/cons/` | gRPC | `grpc.UnaryInterceptor` |

どちらも同じ **Canonical Log Lines** パターン：

1. middleware / interceptor が `eventAttrs` を生成し `context.Context` に注入
2. 各処理層が `event.Add(slog.String("key", "val"))` でフィールドを追加
3. middleware / interceptor がレスポンス返却後に **1行だけ** 出力

```go
// HTTP middleware
func WideEventMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        event := &eventAttrs{}
        ctx := context.WithValue(r.Context(), eventKey{}, event)
        next.ServeHTTP(w, r.WithContext(ctx))
        logger.Info("request", event.attrs...)  // 1行だけ
    })
}

// gRPC interceptor — 構造は全く同じ
func WideEventInterceptor(
    ctx context.Context, req interface{},
    info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (interface{}, error) {
    event := &eventAttrs{}
    ctx = context.WithValue(ctx, eventKey{}, event)
    resp, err := handler(ctx, req)
    logger.Info("request", event.attrs...)  // 1行だけ
    return resp, err
}
```

## trace_id の伝播

OTel のコンテキスト伝播は使わない。自前で紐付ける。

```
tier1: trace_id を UUID で生成
  |
  |-- HTTP版:  X-Trace-Id ヘッダで tier2 に渡す
  |-- gRPC版:  metadata.Pairs("x-trace-id", ...) で tier2 に渡す
  |
  '-- Queue:   メッセージボディに埋め込む → cons がパースして取り出す
```

## クイックスタート

### Docker Compose（HTTP版）

```bash
make up        # docker compose up -d --build
make logs      # ログ収集開始
curl http://localhost:8100/fast
curl http://localhost:8100/slow
curl http://localhost:8100/random
curl http://localhost:8100/error
make query Q=queries/q1_slow_routes.sql
make down
```

### ローカル実行（Docker なし）

MySQL と RabbitMQ をインストール済みの環境で：

```bash
# tier2
DB_DSN="root:rootpass@tcp(127.0.0.1:3306)/demo" PORT=8400 ./tier2/tier2 > logs/tier2.ndjson &

# cons
DB_DSN="root:rootpass@tcp(127.0.0.1:3306)/demo" \
QUEUE_URL="amqp://guest:guest@127.0.0.1:5672/" \
./cons/cons > logs/cons.ndjson &

# tier1
TIER2_URL="http://127.0.0.1:8400" \
QUEUE_URL="amqp://guest:guest@127.0.0.1:5672/" \
./tier1/tier1 > logs/tier1.ndjson &

curl http://localhost:8080/fast
```

### gRPC版

```bash
# tier2 (gRPC server)
DB_DSN="root:rootpass@tcp(127.0.0.1:3306)/demo" PORT=9400 ./grpc/tier2/tier2 &

# cons
DB_DSN="root:rootpass@tcp(127.0.0.1:3306)/demo" \
QUEUE_URL="amqp://guest:guest@127.0.0.1:5672/" \
./grpc/cons/cons &

# tier1 (HTTP server, calls tier2 via gRPC)
TIER2_ADDR="127.0.0.1:9400" \
QUEUE_URL="amqp://guest:guest@127.0.0.1:5672/" \
./grpc/tier1/tier1 &
```

## DuckDB クエリ例

```bash
duckdb < queries/q1_slow_routes.sql    # どのルートが遅い？
duckdb < queries/q2_saas_correlation.sql  # SaaS障害と遅延の相関
duckdb < queries/q3_db_bottleneck.sql  # DBボトルネック時間帯
duckdb < queries/q4_trace_join.sql     # trace_id でサービス横断追跡
```

### Q4: trace_id JOIN の例

```sql
SELECT
    t1.trace_id, t1.route,
    t1.duration_ms AS tier1_ms,
    t2.duration_ms AS tier2_ms,
    t2.db_duration_ms
FROM read_ndjson_auto('logs/tier1.ndjson') t1
JOIN read_ndjson_auto('logs/tier2.ndjson') t2
  ON t1.trace_id = t2.trace_id
ORDER BY t1.time DESC LIMIT 20;
```

## リポジトリ構成

```
wide-events-demo/
|-- compose.yml
|-- Makefile
|-- tier1/                  # HTTP版 tier1
|   |-- main.go
|   '-- Dockerfile
|-- tier2/                  # HTTP版 tier2
|   |-- main.go
|   '-- Dockerfile
|-- cons/                   # HTTP版 queue consumer
|   |-- main.go
|   '-- Dockerfile
|-- grpc/                   # gRPC版
|   |-- proto/tier2.proto
|   |-- tier1/              # HTTP server + gRPC client
|   |-- tier2/              # gRPC server + interceptor
|   '-- cons/               # queue consumer (同一)
|-- waf/haproxy.cfg
|-- web/nginx.conf
|-- db/bootstrap.sql
|-- queries/q{1..4}_*.sql
'-- logs/                   # .gitignore 対象
```

## 依存

- Go 1.22+
- MySQL
- RabbitMQ
- DuckDB（クエリ実行用）

Go ライブラリ:
- `log/slog` (標準ライブラリ)
- `github.com/google/uuid`
- `github.com/rabbitmq/amqp091-go`
- `github.com/go-sql-driver/mysql`
- `google.golang.org/grpc` (gRPC版のみ)

## クレジット

アーキテクチャ構成は [gkspranger/o11y-demo](https://github.com/gkspranger/o11y-demo) をベースにしています。
WAF (HAProxy)、Web (Nginx)、DB (MySQL) の設定ファイルは同リポジトリから流用しています。
