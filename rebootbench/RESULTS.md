# Phase 0 結果

実行: 2026-06-28, Docker 29.1.3 / WSL2 / Linux 5.15

## 環境上の発見

**Docker 29.x では `--restart=always` が `docker kill` 後に発火しない**
(restart_count=0、コンテナは Exited (137) のまま放置される)。
これは plan の F3 前提 (「docker の restart policy 前提」) と食い違うため、
`internal/injector/docker.go` で kill 直後に非同期で `docker start` を発行する形に変更した。
これにより測るのは「kill → 外部観察者の restart 命令 → first 200」の合計時間となる。

## 実験サマリ

| experiment | SUT | probe interval | N | min | p50 | p95 | p99 | max |
|---|---|---|---:|---:|---:|---:|---:|---:|
| 427295b4 | nginx:alpine | 50ms | 30 | 48.19ms | 49.25ms | 49.39ms | 50.28ms | 50.28ms |
| e1f74135 | nginx:alpine | 10ms | 30 | 8.25ms  | 9.28ms  | 9.37ms  | 10.25ms | 10.25ms |
| bab49806 | httpd:alpine | 10ms | 30 | 7.35ms  | 9.27ms  | 9.41ms  | 9.41ms  | 9.41ms  |

## 仮説の検証 (plan 0.9 より)

| 仮説 | 結果 |
|---|---|
| H0-1: nginx の recovery time は中央値 1秒以内 | ✅ 9〜49ms と桁違いに速い |
| H0-2: trial 間で variance が大きい | ❌ ほぼ無い (probe interval に律速されているため) |
| H0-3: 最初の trial は他より遅い (warm-up効果) | ❌ trial_index=0 と他で差なし (CSV参照) |
| H0-4: probe interval が結果に影響する (測定限界) | ✅ **明確に検証**: 50ms→p50≈49ms、10ms→p50≈9ms |
| H0-5: docker kill から first probe failure までに遅延がある | ⏸ 未分析 (probe テーブルから別途算出可) |

## 主要な発見

1. **計測限界が probe interval に決まる**: nginx と httpd を区別できない。両者とも
   「次の probe 発射まで」が支配的で、真の recovery time はその中に埋もれている。
2. **真の recovery time は < 10ms** と推定: interval を下げるごとに p50 が比例して下がる。
3. **stateless サービスの再起動はミリ秒オーダー**: Docker daemon の `docker start`
   発行から HTTP 200 を返すまで、nginx・httpd ともに 10ms 以下。

## Phase 1 への含意

- probe を **pull(interval)** から **push(連続 TCP connect / 連続 GET pipeline)** に
  変える必要がある。または interval を 1ms 以下に下げる。
- 状態を持つ SUT (postgres など) を測るには **TCP probe や独自プロトコル probe** が
  必要 (HTTP のみでは無理)。Phase 2 の data probe 設計と合わせて検討。
- **Docker restart policy の挙動はバージョン依存**。Phase 1 で injector 抽象化する
  際は「kill のみ」「kill+restart」「graceful stop」を分離した方がよい。

## 完了基準チェック (plan 0.10)

- [x] `rebootbench phase0` コマンドが動く
- [x] 30 trial の実験が完了し、SQLite に永続化される
- [x] 集計結果が標準出力と CSV で出る
- [x] nginx ともう1つの SUT (httpd) で実行 — 差分はゼロという観察結果
- [x] README にビルドと実行手順がある
- [x] git で `phase0` タグを切る
