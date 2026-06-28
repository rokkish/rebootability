# Phase 0 結果

実行: 2026-06-28, Docker 29.1.3 / WSL2 / Linux 5.15

## 環境上の発見

**Docker 29.x では `--restart=always` が `docker kill` 後に発火しない**
(`restart_count=0`、コンテナは `Exited (137)` のまま放置される)。
plan の F3 前提 (「docker の restart policy 前提」) が当該環境では成立しない。
これを「環境が持つ自己復活力ゼロ」という測定結果として扱うため、Injector を
**3 モード** (`kill` / `kill-start` / `restart`) に分けた。詳細は README または
plan の F3。

## 計測ロジックのバグと修正

最初の Phase 0 計測は「inject 後の最初の 200 を recovery とする」だったが、これは
`docker kill` CLI が返るまでの数百ms 〜 1 秒の間に発射された probe が、まだ生きて
いる SUT にヒットして 200 を返す現象を **「9ms で復活」と誤判定** していた。

修正: recovery = "inject 後に少なくとも 1 回失敗 (err or non-2xx) を観測した後の、
最初の 200"。これにより、当該環境の真の値は **約 1.5 秒** であることが分かった
(従来計測は 100 倍以上のオーダーで誤っていた)。

## 修正後の実験 (5 trial × 3 モード)

probe interval = 10ms, probe-timeout = 8ms, pre-settle = 500ms, post-settle = 300ms

### `kill` モード

| exp | trial | status | recov_ms |
|---|---:|---|---:|
| 18a96088 | 0 | (旧バグ版で計測) | 9 |
| 18a96088 | 1〜2 | `pre_settle_failed` (auto-restart なし) | — |

**観察**: Docker 29 では trial 1 以降コンテナが死んだまま。これは「環境の自己復活力ゼロ」
そのもの。バグ修正後に再実行すれば trial 0 も `no_recovery` で記録されるはず (未取得)。

### `kill-start` モード (5 trial, 修正後)

| trial | start_lag_ms | recov_ms | start→200_ms |
|---:|---:|---:|---:|
| 0 | 792 | 1330 |  538 |
| 1 | 895 | 1699 |  805 |
| 2 | 892 | 1649 |  757 |
| 3 | 828 | 1589 |  762 |
| 4 | 956 | 1710 |  754 |
| **p50** | **892** | **1649** | **757** |

**分解**: docker kill CLI が返るまで ~900ms (コンテナの実際の exit 検出を含む)、
その後 docker start から first 200 まで ~750ms。

### `restart` モード (30 trial, cooldown=5s, pre-settle=800ms, grace=0) — 本実験

| 統計 | 値 |
|---|---:|
| N (completed) | 29/30 |
| min  | 1.459s |
| p50  | 1.560s |
| mean | 1.582s |
| p95  | 1.680s |
| p99  | 1.969s |
| max  | 1.969s |

(experiment `0e02ef27`)

### `restart` モード (5 trial, grace=0, 修正後)

| trial | recov_ms |
|---:|---:|
| 0 | 1370 |
| 1 | 1560 |
| 2 | 1539 |
| 3 | 1550 |
| 4 | 1679 |
| **p50** | **1550** |

**観察**: `docker restart -t 0` は一発 CLI で完結。total は kill-start と概ね同じ
スケール (1.3〜1.7s)。grace=0 でも kill→start に 1.5s 程度かかる = SUT 起動 +
docker daemon の処理。

## モード間の意味づけ (改めて)

| モード | 何を測ったか (今回の数字で) |
|---|---|
| `kill` | この環境では SUT 自動復活力ゼロ (= 1 trial だけ生き残って後は死亡) |
| `kill-start` | 突然死 + 外部観察者が即時 (delay=0) 再起動命令 → 1.65s |
| `restart` | 計画的再起動コスト → 1.55s |

## 仮説の検証 (plan 0.9 より)

| 仮説 | 結果 |
|---|---|
| H0-1: nginx の recovery time は中央値 1秒以内 | ❌ 実測 1.5〜1.7s。CLI/daemon オーバーヘッドが支配的 |
| H0-2: trial 間で variance が大きい | △ 1.3〜1.7s、CV は 10% 程度。「大きい」とまでは言えない |
| H0-3: 最初の trial は他より遅い (warm-up効果) | ❌ trial 0 がむしろ最速 (キャッシュ等の影響?) |
| H0-4: probe interval が結果に影響する (測定限界) | ✅ (旧バグ含めて) 真の recovery が ~1.5s なら 10ms interval で十分。元の 9ms は計測バグだった |
| H0-5: docker kill から first probe failure までに遅延がある | ✅ 〜数百 ms 間は 200 が返り続けることをまさに観測 (それがバグの原因) |

## バーストエラー: 短い cooldown で Docker が縮退

`cooldown=2s, pre-settle=500ms` の最初の 30 trial 実験 (`restart` モード) は **15/30**
しか完走しなかった。失敗パターンは**バースト的** (例: trial 0,1,2 失敗 → 3-13 成功
→ 14-23 連続 10 失敗 → 24-29 復活)。

失敗 trial の probe を見ると、pre-settle 中の全 50 probe (500ms / 10ms) が
**`read: connection reset by peer`** を返している。docker-proxy のポートは生きて
いる (TCP SYN/ACK は通る → latency 1-1.5ms で即 reset) が、nginx 自体がまだ
正常応答できない、という状態が 500ms 以上続く。

`cooldown=5s, pre-settle=800ms` に伸ばすと 29/30 完走。**rapid kill cycle で Docker
の再起動メカニズムが縮退する**ことが分かった。意味としては:

- ベンチマークとして、cooldown が短すぎると **SUT 由来でない**追加遅延が混入する
- 逆に長い cooldown を要する事実そのものが、SUT (or 環境) のレジリエンス特性の
  1 指標になり得る — 「N 秒以内に連続再起動すると壊れる」

Phase 1 以降では `--cooldown` の感度分析を入れる価値あり (plan 0.12 の意思決定
ポイント「次に何を測りたいか」に対する具体的な答えの一つ)。

## 主要な発見

1. **`docker kill` CLI が返るまで ~900ms かかる**: コンテナ ID 解決、SIGKILL 送信、
   コンテナの exit を kernel が確認する間、CLI はブロックする。この間 SUT は
   応答し続けるため、recovery 判定で「失敗を 1 回観察」を要求する設計が必須。
2. **stateless サービスの復活でも 1.5s かかる**: nginx :alpine という最軽量の例で
   さえ。Docker の overhead が支配的で、SUT 自体の起動は誤差レベル。
3. **計画的再起動 (`restart`) と突然死再起動 (`kill-start`) はほぼ同じ時間**:
   なぜなら grace=0 にした `restart` は内部的に kill+start を atomic に行うだけで、
   外から CLI を 2 回呼ぶ `kill-start` と本質的に同じ作業をしている。
4. **「自動復活ありき」の前提は環境ごとに崩れる**: Docker 29 の挙動は plan を書いた
   時点の前提 (restart policy 前提) と違った。Phase 1 で k8s / process / pod の各
   injector を作るときも「自動復活が動くか」をまず確認する probe が必要。
5. **rapid kill cycle で Docker が縮退する** (上節)。ベンチマーク自体の感度分析
   軸として `cooldown` が浮上。

## Phase 1 への含意

- **Injector 抽象化はもう存在する** (3 モード)。Phase 1 は新しい SUT 環境
  (k8s / systemd / raw process) に対して 3 モード相当を実装する作業に集中できる。
- **計測バグの教訓**: recovery 判定は「失敗を見てから 200」が必須。これは Phase 2
  以降の data probe (sequence チェック等) でも同様に「失敗のシグナル」が必要。
- **state を持つ SUT (postgres) の測定** はまだ未着手 (HTTP probe しか持っていない)。
  Phase 2 の data probe と合わせて TCP/プロトコル probe を作る必要がある。

## Phase 0.5: cooldown 感度分析

cooldown を 1s / 2s / 3s / 5s / 8s と変えて、各 30 trial を `restart` モードで計測
(他の条件は固定: interval=10ms, pre-settle=800ms, post-settle=300ms, grace=0)。
スクリプトは `experiments/cooldown_sweep.sh`。

### 結果

| cooldown | 完走率 | min ms | avg ms | max ms |
|---|---:|---:|---:|---:|
| 1s | 17/30 = **57%** | 1210 | 1425 | 1689 |
| 2s | 18/30 = **60%** | 1229 | 1559 | 1788 |
| 3s | 24/30 = **80%** | 1279 | 1579 | 1739 |
| 5s | 30/30 = **100%** | 1219 | 1538 | 1619 |
| 8s | 30/30 = **100%** | 1229 | 1527 | 1690 |

### 観察

1. **完走率は cooldown=3〜5s の間で急上昇** (sigmoid 的)。Docker daemon + WSL2 の
   組合せでは、5 秒の cooldown が「次の kill が前回の再起動と干渉しなくなる」閾値。
2. **完走 trial の recovery time 自体は cooldown にほぼ依存しない** (~1.5s)。
   = 「復活時間そのもの」と「benchmark の安定実行に必要な間隔」は別物。
3. **短い cooldown では avg recovery が小さく見える** (1s で 1425ms, 5s で 1538ms)。
   これは選択バイアス: 早く戻る trial だけが pre-settle を通り、遅い trial は
   `pre_settle_failed` で recovery 統計から消えるため。**ベンチマーク数字を
   読むときは完走率も併記しないと誤誘導する**。

### 含意

- ベンチマークの「報告すべき数字」は (cooldown, 完走率, recovery 統計) の 3 つ組
  で意味を持つ。
- Phase 1 以降の SUT 比較では「等しい cooldown かつ等しく 100% 完走の領域」で
  比較する、もしくは「100% 完走に必要な最小 cooldown」自体を比較軸にする選択肢
  もある。後者は「環境の再起動連続耐性」を捉える独立の指標になり得る。
- Recovery Time (RT) 単一指標で SUT を順位付けることの危うさが、Phase 0.5 で
  実機データとして可視化された。これは plan の RT/DI/BR/RC の **4 軸スコア化**
  (Phase 3) を急ぐ動機になる。

## 完了基準チェック (plan 0.10)

- [x] `rebootbench phase0` コマンドが動く
- [x] 30 trial の実験が完了し、SQLite に永続化される (restart モード 29/30 完走, experiment `0e02ef27`)
- [x] 集計結果が標準出力と CSV で出る
- [x] 3 モードで nginx を計測し、差分 (というより類似) を観察
- [x] README にビルドと実行手順がある
- [x] git で `phase0` タグを切る (phase0 タグは旧バグ版を指す。修正版は次のタグ `phase0-fix1` 等を検討)
