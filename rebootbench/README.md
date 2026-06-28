# rebootbench

再起動可能性 (rebootability) を外部観察者として測るツール。
Phase 0 の MVP: Docker コンテナを `docker kill` で落とし、HTTP probe で復活までの時間を測定して SQLite に永続化する。

## 前提

- Go 1.23+
- Docker (デーモン稼働中)
- SUT は HTTP で `200` を返すサービスであり、Docker の restart policy で自動復帰すること

## ビルド

```sh
go build -o rebootbench .
```

## 使い方

SUT として nginx を起動:

```sh
docker run -d --name rebootbench-nginx --restart=always -p 18080:80 nginx:alpine
```

実験を実行 (30 trial、デフォルト設定):

```sh
./rebootbench phase0 \
  --container rebootbench-nginx \
  --url http://localhost:18080/ \
  --interval 50ms \
  --trials 30
```

主要フラグ:

| flag | default | 意味 |
|---|---|---|
| `--container` | `rebootbench-nginx` | docker kill 対象 |
| `--url` | `http://localhost:18080/` | probe URL |
| `--interval` | `50ms` | probe 間隔 |
| `--probe-timeout` | `30ms` | 1 probe の HTTP timeout |
| `--trials` | `30` | trial 回数 |
| `--pre-settle` | `1s` | 注入前に probe を回す時間 (1回以上 200 が必要) |
| `--post-settle` | `1s` | 復活検出後に probe を回す時間 |
| `--cooldown` | `5s` | trial 間の待ち時間 |
| `--recovery-timeout` | `30s` | 復活待ちの上限 |
| `--db` | `rebootbench.db` | SQLite ファイル |
| `--csv` | (自動) | recovery_time の CSV 出力先 |

## 出力

- `rebootbench.db`: SQLite ファイル (テーブル: `experiment`, `trial`, `probe`)
- `<experiment_id>.csv`: trial_index ごとの recovery_time_ns
- 標準出力: N / min / p50 / mean / p95 / p99 / max

## 設計の要点

- **Injector / Observer / Recorder の3層**: Phase 1 以降で injector のバリエーション (process, pod, node) を足せるよう分離。
- **probe を即時 INSERT**: SUT 再起動中に観察プロセスが死んでも、それまでに観察した結果は SQLite に残る (WAL モード、`synchronous=NORMAL`)。
- **時刻は nanoseconds**: `time.Now().UnixNano()` で保存。同一ホストの単調時計に依存 (Phase 2 で NTP/PTP 検討)。
- **recovery の定義**: 「`inject_at` 以後の最初の `200` レスポンスの `SentAt`」。連続 N 個の 200 を要求する変種は Phase 0 範囲外。

## 既知の制約 (Phase 0)

- docker のみ対応 (Phase 1 で injector 抽象化)
- HTTP probe のみ (Phase 2 で data integrity probe)
- recorder は同一ホスト (Phase 1 でリモート対応)
- 集計は手動分析前提 (Phase 4 で自動化)

## SQL での確認例

```sh
sqlite3 rebootbench.db "
SELECT trial_index, recovery_time_ns/1e6 AS recovery_ms, status
FROM trial WHERE experiment_id = (SELECT id FROM experiment ORDER BY started_at DESC LIMIT 1)
ORDER BY trial_index;"
```
