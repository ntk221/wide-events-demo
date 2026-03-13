---
title: "構造化ログで実装する Wide Events — \"問えなかった問い\" に SQL で答える"
emoji: "🔍"
type: "tech"
topics: ["observability", "go", "duckdb", "logging"]
published: false
---

## 1. 障害対応の現実

ある日の障害対応を想像してください。本番環境のレイテンシが急上昇しています。アラートが飛んできたものの、どのエンドポイントが影響を受けているのか、何が原因なのかはアラートからは分かりません。手がかりを求めてログを開くと、大量の行が流れています。でも、1行1行はリクエスト処理の断片でしかありません。「外部 API の呼び出しが遅い」と「レイテンシが悪化している」を結びつけるには、ログを横断して手作業で組み立てるしかありません。

---

## 2. 探索的なログ調査の流れ

障害対応で本当に欲しいのは、こういう探索の流れです：

```sql
-- 1. まず遅いリクエストを眺める
SELECT * FROM events WHERE duration_ms > 1000 LIMIT 100

-- 2. saas_ok が気になった → そのまま掘る
SELECT saas_ok, AVG(duration_ms) FROM events GROUP BY saas_ok

-- 3. route も気になった → フィールドを足すだけ
SELECT saas_ok, route, AVG(duration_ms) FROM events GROUP BY saas_ok, route
```

各ステップが前のクエリの気づきから自然につながります。テーブルをまたぐ設計判断が不要です。実装の内部知識も不要です。データを見て、気になったものを掘る。それだけです。

---

## 3. request_id の JOIN は探索的なログ調査に合わない

よくあるログは以下のようになっているのではないでしょうか：

```
2024-03-08 11:34:33 [INFO] Received request: POST /checkout
2024-03-08 11:34:33 [INFO] Calling SaaS API...
2024-03-08 11:34:38 [WARN] SaaS API timeout after 5000ms
2024-03-08 11:34:38 [INFO] Enqueuing message to RabbitMQ
2024-03-08 11:34:38 [INFO] Calling tier2 gRPC...
2024-03-08 11:34:38 [INFO] Response sent: 500 in 5023ms
```

処理ステップごとに1行ずつ出力するログです。このようなログでも以下のように request_id を付与して JOIN すれば、遅いリクエストの原因を突き止めることはできます。

```sql
SELECT s.saas_ok, w.route, AVG(w.duration_ms)
FROM web_logs w
JOIN saas_logs s ON w.request_id = s.request_id
GROUP BY s.saas_ok, w.route
```

ただし、このやり方では2章で述べた探索的なログ調査は成り立ちません。

この JOIN を書くには、3つのことを知っている必要があります。

1. `saas_logs` というテーブルが存在すること
2. そこに `saas_ok` というフィールドがあること
3. `request_id` で紐付けられること

つまり、中でどういうログを吐いているかの実装を把握していないと、そもそも何と突き合わせるかが書けません。「`saas_ok` はどのテーブルにあるんだっけ？」——この一手間が、障害時の探索のリズムを断ち切ります。

これは LLM を使った分析でも同じです。ステップごとのログを AI に分析させようとすると、「どのテーブルに何のフィールドがあるか」というコードベースのコンテキストも一緒に渡す必要があります。

自分が書いていないコードの障害対応や、しばらく触っていないサービスの調査では、この問題はさらに深刻になります。

---

## 4. Wide Events — 1リクエスト = 1行

Wide Events は、この問題に対するシンプルな回答です。**1リクエストの処理全体を、1行の構造化された JSON に集約して出力します。**

```json
{
  "service": "api",
  "trace_id": "69c29fc1-dc14-448e-a1c8-07d58593e754",
  "route": "/checkout",
  "duration_ms": 1823,
  "saas_ok": false,
  "saas_duration_ms": 5001,
  "db_ok": true,
  "db_duration_ms": 3,
  "queue_ok": true,
  "error": true
}
```

この1行に、ルーティング、外部 API の結果、DB の応答時間、キューの状態 ── リクエストに関わる全情報が入っています。

`SELECT *` で全フィールドが見えるので、2章で述べた探索のフローがそのまま成り立ちます。実装を知らなくても「あ、`saas_ok` というフィールドがあるな」と気づくことができる構造になっています。気になったフィールドを `GROUP BY` に足していくだけで、問いを掘り下げていけます。もちろん AI も同じように `SELECT *` から探索を始められます。

Wide Events によってログ設計の意思決定コストも変わります。従来は「このフィールドは記録する価値があるか？」という取捨選択が毎回発生していましたが、Wide Events では**とりあえず必要そうなものは全部入れて、不要なら読む時に無視すればよい**からです。

---

## 5. Wide Events の系譜

このアイデアは複数の場所で独立に発展してきました。

### Stripe の Canonical Log Lines

2016年、Stripe のエンジニア Brandur Leach が [Canonical Log Lines](https://brandur.org/canonical-log-lines) として紹介した。通常のログとは別に、リクエスト処理の最後に全情報を1行にまとめて出力するパターンです。2019年には Stripe の公式ブログでも [取り上げられ](https://stripe.com/blog/canonical-log-lines)、「これがなければ目隠しで飛んでいるようなもの（flying blind）」と表現されています。

### Charity Majors の Structured Events

Honeycomb の CTO、Charity Majors は [Live Your Best Life With Structured Events](https://charity.wtf/2022/08/15/live-your-best-life-with-structured-events/) で、同じアイデアをさらに推し進めました。1リクエスト = 1イベント。数百のフィールドを持つ、「任意に幅広い（arbitrarily wide）」構造化イベント。Honeycomb で成熟したデータセットは200〜500次元にもなるといいます。

> 何か役に立つかもと思ったら、迷わず突っ込め。sticky buns がホコリを集めるように、コンテキストをどんどん集めろ。
> — Charity Majors

### A Practitioner's Guide to Wide Events

2024年、Jeremy Morrell が [A Practitioner's Guide to Wide Events](https://jeremymorrell.dev/blog/a-practitioners-guide-to-wide-events/) として実践ガイドを書きました。「データ量が心配」という懸念に対して、OLAP の列指向圧縮により Wide Events は見た目ほどストレージを消費しないことを示しています。

---

## 6. Canonical Log Lines として実装する

「全部ぶち込め」というメンタルモデルは魅力的ですが、実装上の課題があります。Majors 自身が「pain in the ass」と認めているように、何をぶち込めるかはコードのどこにいるかによって違う。

HTTP ハンドラからはルーティング情報が見えますが、DB 層からは見えません。逆に DB のクエリ結果は DB 層にしかありません。リクエスト処理のあちこちに散らばった情報を、最終的に1行にまとめる必要があります。

この課題への実装上の答えが Canonical Log Lines パターンです。

```
1. リクエスト開始時に「イベント属性バッグ」を作ります
2. context 経由で引き回します
3. 各処理層がフィールドを追加します
4. リクエスト完了時に、バッグの中身を1行の JSON として出力します
```

Go の `context.Context` と `log/slog` でこれを実現する方法を、デモプロジェクトで見ていきます。

---

## 7. デモプロジェクトの紹介

Wide Events を体験するためのデモプロジェクトを用意しました。

https://github.com/ntk221/wide-events-demo

### アーキテクチャ

```
[browser] → [waf] → [web] → [tier1] → [saas]  (mock)
                                     → [queue] → [cons] → [db]
                                     → [tier2] → [db]
```

| サービス | 技術 | 役割 |
|---------|------|------|
| waf | HAProxy | エントリポイント |
| web | nginx | リバースプロキシ |
| tier1 | Go HTTP | リクエスト受付、下流呼び出し |
| tier2 | Go gRPC | DB クエリ |
| cons | Go | キューの非同期コンシューマー |
| db | MySQL | データストア |
| queue | RabbitMQ | メッセージキュー |
| saas | Go HTTP | 外部 SaaS モック（障害注入付き） |

HTTP・gRPC・非同期キューの3パターンで、同じ Wide Events の実装パターンを使っています。

### 実装の核：eventAttrs と WideEventMiddleware

tier1 の実装を見てみましょう。まず「イベント属性バッグ」にあたる構造体：

```go
type eventAttrs struct {
    mu    sync.Mutex
    attrs []slog.Attr
}

func (e *eventAttrs) Add(attrs ...slog.Attr) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.attrs = append(e.attrs, attrs...)
}
```

mutex で保護された `[]slog.Attr` のスライスです。各処理層がスレッドセーフにフィールドを追加できます。

そして、HTTP ミドルウェア（以下は要点を示す簡略化した疑似コードで、概念図です。動作を保証するものではありません）：

```go
func WideEventMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        traceID := uuid.New().String()
        event := &eventAttrs{}
        event.Add(
            slog.String("service", "tier1"),
            slog.String("trace_id", traceID),
            slog.String("route", r.URL.Path),
        )
        ctx := context.WithValue(r.Context(), eventKey{}, event)

        next.ServeHTTP(w, r.WithContext(ctx))

        event.Add(slog.Int64("duration_ms", time.Since(start).Milliseconds()))
        logger.Log(context.Background(), level, "request", args...)
    })
}
```

リクエストの最初に `eventAttrs` を作り、context に注入します。ハンドラと全ての下流呼び出しがフィールドを追加します。リクエスト完了時に、蓄積されたフィールドを1行の JSON として出力します。

例えば、外部 SaaS API の呼び出し部分はこうなります（同様に疑似コード）：

```go
func doSaas(ctx context.Context, hasError *bool) {
    start := time.Now()
    event := ctx.Value(eventKey{}).(*eventAttrs)

    resp, err := client.Get(saasURL)
    durationMs := time.Since(start).Milliseconds()

    event.Add(
        slog.Bool("saas_ok", ok),
        slog.Int("saas_status", resp.StatusCode),
        slog.Int64("saas_duration_ms", durationMs),
    )
}
```

context からイベント属性バッグを取り出して、結果を追加するだけです。「全部ぶち込め」が1行で実現できます。

### gRPC でも同じパターン

tier2 では gRPC の `UnaryServerInterceptor` で同じことをしています。HTTP ミドルウェアの代わりにインターセプターを使うだけで、パターンは同一です（疑似コード）：

```go
func WideEventInterceptor(
    ctx context.Context,
    req interface{},
    info *grpc.UnaryServerInfo,
    handler grpc.UnaryHandler,
) (interface{}, error) {
    start := time.Now()
    event := &eventAttrs{}
    event.Add(
        slog.String("service", "tier2"),
        slog.String("trace_id", traceID),
        slog.String("method", info.FullMethod),
    )
    ctx = context.WithValue(ctx, eventKey{}, event)

    resp, err := handler(ctx, req)

    event.Add(slog.Int64("duration_ms", time.Since(start).Milliseconds()))
    logger.Log(context.Background(), level, "request", args...)
    return resp, err
}
```

trace_id は gRPC metadata の `x-trace-id` ヘッダーで伝播されます。

### 出力されるログの実物

tier1 が出力する Wide Event の実物：

```json
{
  "time": "2026-03-08T11:34:33.53322175Z",
  "level": "INFO",
  "msg": "request",
  "service": "tier1",
  "trace_id": "69c29fc1-dc14-448e-a1c8-07d58593e754",
  "route": "/fast",
  "was_slow": false,
  "queue_ok": true,
  "queue_duration_ms": 0,
  "saas_ok": true,
  "saas_status": 200,
  "saas_duration_ms": 1,
  "tier2_ok": true,
  "tier2_duration_ms": 8,
  "duration_ms": 9
}
```

1行に、ルート・キューの成否・SaaS の応答・tier2 の応答時間・全体のレイテンシが入っています。

### 動かし方

```bash
git clone https://github.com/ntk221/wide-events-demo.git
cd wide-events-demo
make up       # 全サービス起動
make traffic  # テストトラフィック生成（20リクエスト × 4ルート）
make logs     # ログ収集
```

---

## 8. クエリで体感する

DuckDB で実際にクエリを実行してみましょう。Wide Events は NDJSON（1行1JSON）で出力されているので、DuckDB の `read_ndjson_auto()` でそのまま読めます。

### まずは探索から

2章で述べた「`SELECT *` → 気づき → 掘る」の探索フローをやってみましょう。

```sql
-- まず遅いリクエストを眺める
SELECT * FROM read_ndjson_auto('logs/tier1.ndjson')
WHERE duration_ms > 1000
LIMIT 5;
```

全フィールドが見えます。`was_slow`, `saas_ok`, `tier2_duration_ms` ── 何が記録されているか、実装を知らなくても分かります。ここから気になったフィールドを掘っていけます。

### Q1: どのルートが遅い？

これはステップごとのログでも答えられる問いです。各ログ行にルートと応答時間があれば十分なので、比較のための出発点として使います。

```sql
SELECT
    route,
    COUNT(*)         AS count,
    AVG(duration_ms) AS avg_ms,
    MAX(duration_ms) AS max_ms
FROM read_ndjson_auto('logs/tier1.ndjson')
GROUP BY route
ORDER BY avg_ms DESC;
```

```
┌─────────┬───────┬─────────┬────────┐
│  route  │ count │  avg_ms │ max_ms │
├─────────┼───────┼─────────┼────────┤
│ /slow   │    20 │  1501.6 │   1507 │
│ /fast   │    21 │   219.0 │   1504 │
│ /random │    20 │   154.1 │   1504 │
│ /error  │    20 │    78.7 │   1501 │
└─────────┴───────┴─────────┴────────┘
```

`/slow` が圧倒的に遅い。

### Q2: SaaS の障害はレイテンシに影響しているか？

ここから話が変わります。この問いに答えるには、`saas_ok`（外部 API の成否）と `duration_ms`（全体のレイテンシ）が同じ行にある必要があります。ステップごとのログだと、SaaS の呼び出し結果は `saas_logs` テーブルに、全体のレイテンシは `web_logs` テーブルにあります。JOIN が必要で、しかも「`saas_ok` がどのテーブルにあるか」を知っていないと書けません。

Wide Events なら：

```sql
SELECT
    saas_ok,
    COUNT(*)         AS count,
    AVG(duration_ms) AS avg_ms
FROM read_ndjson_auto('logs/tier1.ndjson')
GROUP BY saas_ok;
```

正常時：

```
┌─────────┬───────┬────────┐
│ saas_ok │ count │ avg_ms │
├─────────┼───────┼────────┤
│ true    │    81 │  485.0 │
└─────────┴───────┴────────┘
```

障害発生後（`make saas-down` → `make traffic` → `make saas-up`）：

```
┌─────────┬───────┬──────────┐
│ saas_ok │ count │  avg_ms  │
├─────────┼───────┼──────────┤
│ false   │    80 │  5454.9  │
│ true    │    81 │   485.0  │
└─────────┴───────┴──────────┘
```

SaaS 障害時は平均レイテンシが 11倍に悪化しています。`saas_ok` と `duration_ms` が同じ行にあるから、この1行の SQL で相関が見えます。

### Q3: DB が詰まった時間帯はいつか？

tier2 のログだけで閉じた問いです。`db_duration_ms` が各行にあるので、時間帯ごとに集計するだけで答えが出ます。

```sql
SELECT
    time_bucket(INTERVAL '1 minute', time::TIMESTAMP) AS bucket,
    AVG(db_duration_ms) AS avg_db_ms,
    COUNT(*)            AS count
FROM read_ndjson_auto('logs/tier2.ndjson')
GROUP BY bucket
ORDER BY bucket;
```

```
┌─────────────────────┬───────────┬───────┐
│       bucket        │ avg_db_ms │ count │
├─────────────────────┼───────────┼───────┤
│ 2026-03-08 11:34:00 │       1.2 │    81 │
│ 2026-03-08 11:35:00 │       2.4 │   160 │
└─────────────────────┴───────────┴───────┘
```

時間帯ごとの DB レイテンシの推移が見えます。

### Q4: trace_id でサービスをまたいで追跡する

trace_id を使って、tier1 と tier2 のログを突き合わせます。これは Wide Events でも JOIN が必要ですが、各サービスの Wide Event 同士の JOIN なので、テーブルは2つだけです。ステップごとのログだとテーブル数はサービス内のログ行の種類分だけ増えます。

```sql
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
LIMIT 5;
```

```
┌──────────────────────────┬────────┬──────────┬──────────┬────────────────┐
│         trace_id         │ route  │ tier1_ms │ tier2_ms │ db_duration_ms │
├──────────────────────────┼────────┼──────────┼──────────┼────────────────┤
│ f68c6d66-da30-4fca-...   │ /slow  │     1501 │        0 │              0 │
│ deeba158-6abe-4114-...   │ /slow  │     1502 │        1 │              1 │
│ d69c295f-348d-4d6e-...   │ /slow  │     1501 │        0 │              0 │
│ c5fe61b6-48fe-485f-...   │ /fast  │       12 │        1 │              1 │
│ 9c56ebe6-f77d-486b-...   │ /random│        8 │        0 │              0 │
└──────────────────────────┴────────┴──────────┴──────────┴────────────────┘
```

tier1 で 1500ms かかっているリクエストの tier2 は 0〜1ms。ボトルネックは tier2 ではなく、tier1 であることが分かります。

---

## 9. 障害シミュレーションで体感する

このデモの SaaS サービスは Admin API を持っており、ファイル編集やコンテナ再起動なしで障害を切り替えられる。

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
make logs
make q2
```

結果を並べてみましょう：

| saas_ok | count | avg_ms |
|---------|-------|--------|
| true | 81 | 485.0 |
| false | 80 | 5454.9 |

**SaaS が落ちている間、平均レイテンシが 485ms → 5455ms に悪化しました。** 全リクエストが SaaS のタイムアウト（5秒）を待たされているからです。8章の Q2 と同じクエリ1行で、障害の影響が即座に見えます。

---

## 10. まとめ

Wide Events は「全部ぶち込んで、後でフィルタする」というメンタルモデルです。Canonical Log Lines はその実装パターンで、Go なら `context.Context` と `log/slog` で実現できます。

ステップごとのログを request_id で JOIN して同じ情報を得ることは技術的にはできます。ただし、「誰が・どういう状態で問えるか」が変わります。Wide Events なら `SELECT *` 一発で全フィールドが見えます。実装を知らなくても探索を始められます。

特別なインフラは不要です。構造化ログの延長線上にあります。JSON を吐いて、DuckDB で読みます。それだけで、今まで問えなかった問いに答えられるようになります。

---

### 参考資料

- [Canonical Log Lines](https://brandur.org/canonical-log-lines) — Brandur Leach, 2016
- [Fast and Flexible Observability with Canonical Log Lines](https://stripe.com/blog/canonical-log-lines) — Stripe Engineering Blog, 2019
- [Live Your Best Life With Structured Events](https://charity.wtf/2022/08/15/live-your-best-life-with-structured-events/) — Charity Majors, 2022
- [A Practitioner's Guide to Wide Events](https://jeremymorrell.dev/blog/a-practitioners-guide-to-wide-events/) — Jeremy Morrell, 2024
- [All You Need Is Wide Events, Not "Metrics, Logs and Traces"](https://isburmistrov.substack.com/p/all-you-need-is-wide-events-not-metrics) — Ivan Burmistrov, 2024
- [wide-events-demo](https://github.com/ntk221/wide-events-demo) — 本記事のデモプロジェクト
