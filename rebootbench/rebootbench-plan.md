# rebootbench: 実装計画

再起動可能性 (rebootability) を測定するソフトウェアツールの設計と実装計画。

---

## 0. ゴール

「再起動を観察する外部の観察者」として、SUT (System Under Test) の外側から再起動を**注入**し、その前後を**観察**し、結果を**永続化**するツールを作る。

論文化はサブゴール。**実装によって「どう測るか」から学ぶこと**が主目的。

---

## 1. アーキテクチャの原則

### 1.1 3つの分離

| 役割 | 責務 | SUT との関係 |
|---|---|---|
| **Injector** | 停止操作を実行 | SUT に対して外部から作用 |
| **Observer** | SUT の応答を観察 | SUT と別ホスト/別namespace |
| **Recorder** | 結果を永続化 | SUT とは独立したライフサイクル |

### 1.2 再起動を生き残る設計

再起動で測定結果が消えないために：

- Recorder プロセスは SUT と**別ホスト** or 別コンテナで動く
- ローカル SQLite (速度) + リモート tail (冗長性) のハイブリッド
- 注入前にデータをflushし、注入後の最初の書き込みで前回のセッションをseal

### 1.3 実装言語

**Go** を選択。理由:
- シングルバイナリ配布
- cgo なしで cross-compile
- k8s/docker SDK が充実
- devops ツールエコシステムと相性

---

## 2. 全体ロードマップ

| Phase | 期間 | 範囲 | 出力 |
|---|---|---|---|
| **Phase 0** | 週末 | nginx + docker kill、SQLite記録 | 「測れる」感触の獲得、生データ |
| Phase 1 | 1〜2週 | Injector/Observer/Recorder の3層分離、process & container |  最小プロダクト |
| Phase 2 | 3〜4週 | pod, node 階層、data probe、自前 chaos | 階層別測定 |
| Phase 3 | 〜2ヶ月 | 4軸 (RT/DI/BR/RC) スコア化、複数SUT比較 | ベンチマークツール |
| Phase 4 | 〜3ヶ月 | レポート生成、論文Figure自動化 | 論文実験基盤 |

各Phaseは単独でvalue があり、ここで止めても学びは残る設計にする。

---

# Phase 0: 詳細設計

## 0.1 スコープ

**やること**:
- nginx コンテナを docker で起動
- 一定間隔で HTTP GET を叩き続ける
- `docker kill` で nginx を落とす
- 復活までの時間を計測
- 結果を SQLite に永続化
- 1回の実験を最低30回繰り返す
- 中央値と p99 を出力

**やらないこと**:
- 階層の抽象化 (process/container/pod 切り替え)
- k8s 対応
- 負荷生成 (probeのみ)
- data integrity probe
- リモートrecorder
- レポート生成 (生データを残すだけ)

**目標コード行数**: 250〜400 行 (テスト除く)

---

## 0.2 機能要件

### F1: 実験設定
- CLI 引数で SUT (コンテナ名)、probe URL、間隔、繰り返し回数を指定
- 例: `rebootbench phase0 --container nginx --url http://localhost:8080 --interval 100ms --trials 30`

### F2: Probe
- 指定間隔で HTTP GET を発行
- レスポンスのステータスコード、所要時間、エラーを記録
- probe 自身のオーバーヘッドを最小化 (Go の `net/http` を直接使い、keepalive ON)

### F3: Injector
- **3 モードを提供する** (`--injector`):
  - `kill`: `docker kill` のみ。回復は SUT 環境任せ。自動復活がなければ
    `status=no_recovery` を残す。**測りたい軸: RT (環境の自己復活力)**
  - `kill-start`: `docker kill` 直後に外部から `docker start` を発行 (任意の遅延あり)。
    `inject_at` と `start_at` を別に記録する。**測りたい軸: RT (検知+操作+起動)**
  - `restart`: `docker restart -t N` を呼ぶ (SIGTERM → grace → SIGKILL → start を
    daemon が atomic に実行)。**測りたい軸: RC (計画的再起動コスト)**
- 注入時刻 (UTC, nanoseconds) を記録 — モードによって 1〜2 個のタイムスタンプ
- 当初の計画は「`docker kill` 1 種 + restart policy 前提」だったが、Docker 29.x で
  `--restart=always` が `docker kill` 後に発火しないことを実機で発見したため、
  プラン段階で観察者の責務を「kill のみ」と「kill + 再起動命令」に明示的に分けた。
  これは「観察者が SUT に対して何をしているか」を測定セマンティクスに繰り込むため。

### F4: Trial 制御
- 1 trial = (probe開始) → (定常確認) → (inject) → (復活検出) → (定常確認) → (probe停止)
- trial 間で 5秒のクールダウン
- 指定回数まで繰り返し

### F5: 永続化
- SQLite ファイル (`rebootbench.db`) に書き込み
- 1 probe結果 = 1 row
- 1 inject = 1 row
- 実験設定そのものも保存

### F6: 集計
- 全 trial の Recovery Time (= 注入時刻 → 最初の successful probe までの時間) を算出
- 中央値、p50、p95、p99、min、max を表示
- 結果は標準出力 + CSV ファイル

---

## 0.3 データモデル (SQLite)

```sql
-- 実験ごとの設定
CREATE TABLE experiment (
    id TEXT PRIMARY KEY,                    -- UUID
    started_at INTEGER NOT NULL,            -- Unix nanoseconds
    ended_at INTEGER,
    container_name TEXT NOT NULL,
    probe_url TEXT NOT NULL,
    probe_interval_ns INTEGER NOT NULL,
    trial_count INTEGER NOT NULL,
    git_sha TEXT,                           -- rebootbench 自身のバージョン
    notes TEXT
);

-- 各 trial の情報
CREATE TABLE trial (
    experiment_id TEXT NOT NULL,
    trial_index INTEGER NOT NULL,
    injector_mode TEXT NOT NULL,            -- "kill" / "kill-start" / "restart"
    inject_at INTEGER NOT NULL,             -- Unix nanoseconds (停止操作の起点)
    start_at INTEGER,                       -- kill-start モードのみ。外部 start 発行時刻
    first_recovery_at INTEGER,              -- 最初の200応答の時刻
    recovery_time_ns INTEGER,               -- first_recovery_at - inject_at
    status TEXT NOT NULL,                   -- "completed" / "no_recovery" / "timeout" / "pre_settle_failed" / "inject_failed"
    PRIMARY KEY (experiment_id, trial_index)
);

-- 個別 probe 結果 (大量)
CREATE TABLE probe (
    experiment_id TEXT NOT NULL,
    trial_index INTEGER NOT NULL,
    sent_at INTEGER NOT NULL,
    latency_ns INTEGER,                     -- nullable on error
    status_code INTEGER,                    -- nullable on error
    error TEXT                              -- nullable on success
);

CREATE INDEX idx_probe_experiment ON probe (experiment_id, trial_index, sent_at);
```

設計判断:
- nanoseconds で時刻を扱う (millisecond 精度では足りない)
- probe テーブルは大量レコードになる、indexを付けておく
- `status` フィールドで trial の異常終了を表現

---

## 0.4 コード構成

```
rebootbench/
├── go.mod
├── go.sum
├── main.go                    # CLI エントリ
├── internal/
│   ├── experiment/
│   │   └── experiment.go      # 実験オーケストレーション
│   ├── injector/
│   │   └── docker.go          # docker kill
│   ├── observer/
│   │   └── http.go            # HTTP probe
│   ├── recorder/
│   │   └── sqlite.go          # SQLite 永続化
│   └── analyzer/
│       └── stats.go           # 集計
└── README.md
```

最小構成。後の Phase で injector/, observer/ にバリエーションを足せる構造。

---

## 0.5 主要型定義 (Go)

```go
// ProbeResult: 単一 probe の結果
type ProbeResult struct {
    SentAt     time.Time
    Latency    time.Duration
    StatusCode int
    Err        error
}

// Trial: 1回の inject + 観察セッション
type Trial struct {
    Index           int
    InjectAt        time.Time
    FirstRecoveryAt time.Time
    Probes          []ProbeResult
    Status          string
}

// Experiment: 全 trial の集合
type Experiment struct {
    ID            string
    StartedAt     time.Time
    Config        Config
    Trials        []Trial
}

type Config struct {
    ContainerName string
    ProbeURL      string
    ProbeInterval time.Duration
    TrialCount    int
    PreSettleTime time.Duration  // 注入前の定常状態確認時間
    PostTimeout   time.Duration  // 復活待ちタイムアウト
}
```

---

## 0.6 制御フロー

```
main()
  → parseArgs()
  → setupSQLite()
  → exp := newExperiment()
  → recorder.SaveExperiment(exp)
  
  for i := 0; i < trialCount; i++:
      trial := runTrial(i)
      recorder.SaveTrial(trial)
      time.Sleep(cooldown)
  
  → stats := analyze(exp)
  → printStats(stats)
  → writeCSV(stats)


runTrial(i):
  ctx, cancel := context.WithTimeout(...)
  
  probeChan := observer.Start(ctx)         // probe ループ開始 (goroutine)
  
  preSettle(probeChan, 3秒)                // 安定状態を確認
  
  injectAt := injector.Inject()            // docker kill
  
  recoveryAt := waitForRecovery(probeChan) // 最初の200を待つ
  
  postSettle(probeChan, 3秒)               // 復活後の安定確認
  
  cancel()                                 // probe 停止
  
  return Trial{...}
```

---

## 0.7 実装ステップ (週末ハック想定)

### Step 1: 環境準備 (30分)
- Go モジュール初期化
- 依存追加: `database/sql`, `github.com/mattn/go-sqlite3`, `github.com/google/uuid`
- ローカルで nginx コンテナ起動確認: `docker run -d --name nginx --restart=always -p 8080:80 nginx`

### Step 2: HTTP probe 単体 (1時間)
- 100ms 間隔で `http://localhost:8080` を叩くだけのコード
- 標準出力に latency と status を吐く
- timeout は probe interval より短く設定 (50ms)

### Step 3: Docker kill 単体 (30分)
- `os/exec` で `docker kill <name>` を実行
- 終了時刻を ns精度 で取得

### Step 4: 1 trial の統合 (2時間)
- probe を goroutine で回しつつ inject
- 復活検出: status_code == 200 の最初の probe を捕捉
- 結果をメモリに蓄積、終了時にprint

### Step 5: SQLite 永続化 (1時間)
- スキーマ作成
- 各 probe を即時 INSERT (batch ではなく1件ずつ、再起動耐性のため)
- experiment と trial の summary も書く

### Step 6: 複数 trial ループ (1時間)
- 30回繰り返し
- trial 間にクールダウン
- 各 trial 終了時に DB flush

### Step 7: 集計 (1時間)
- SQL で全 trial の recovery_time_ns を取得
- Go で sort して percentile を計算
- 標準出力に表形式で表示

### Step 8: ドキュメントとREADME (30分)
- 使い方、前提、既知の制約

**合計見積もり: 7〜8時間** = 週末1日でMVP完成

---

## 0.8 期待される最初のデータ

nginx (最小構成) で予想:
- Recovery Time (中央値): 0.5〜2秒
- p99: 3〜5秒
- variance: かなり大きいはず (docker daemon の状態次第)

postgres でも同じ実験をすれば、状態を持つことの代償が見える:
- Recovery Time (中央値): 5〜15秒
- p99: 30秒以上の可能性

この**最初の比較**を撮ることが Phase 0 のゴール。

---

## 0.9 Phase 0 で発見したいこと (仮説リスト)

| 仮説 | 検証方法 |
|---|---|
| H0-1: nginx の recovery time は中央値 1秒以内 | 30 trial の median |
| H0-2: trial 間で variance が大きい | std / IQR |
| H0-3: 最初の trial は他より遅い (warm-up効果) | trial_index vs recovery_time |
| H0-4: probe interval が結果に影響する (測定限界) | 100ms と 50ms で別実験 |
| H0-5: docker kill から first probe failure までに遅延がある | inject_at と first_failed_probe の差 |

これらが見えた時点で、Phase 1 の設計判断 (probe 間隔、trial 数、cooldown) がデータドリブンで決まる。

---

## 0.10 Phase 0 完了基準

- [ ] `rebootbench phase0` コマンドが動く
- [ ] 30 trial の実験が完了し、SQLite に永続化される
- [ ] 集計結果が標準出力と CSV で出る
- [ ] nginx と もう1つの SUT (例: postgres) で実行し、差分が観察できる
- [ ] README にビルドと実行手順がある
- [ ] git で `phase0` タグが切られている

---

## 0.11 Phase 0 の意図的な制限 (後の Phase で解決)

| 制限 | 理由 | 対応 Phase |
|---|---|---|
| docker のみ対応 | スコープ管理 | Phase 1 |
| probe は HTTP のみ | 単純化 | Phase 2 |
| data integrity 未検証 | 別の仕組みが要る | Phase 2 |
| trial 間の独立性が弱い (同じコンテナ再利用) | docker restart policy 依存 | Phase 1 |
| recorder は同一ホスト | ネットワーク構成不要 | Phase 1 |
| 時刻同期は同一ホストの単調時計 | 単純化 | Phase 2 (NTP/PTP) |
| 集計は手動分析前提 | 自動化は後 | Phase 4 |

---

## 0.12 Phase 0 終了後の意思決定ポイント

実装後に以下を判断する:

1. **probe interval は十分か?** 100ms で取りこぼしがあるなら 10ms に。
2. **trial 30回は十分か?** variance が大きければ 100回必要かも。
3. **recovery の定義は妥当か?** 「最初の200」だけでなく「連続Nの200」が必要かも。
4. **Go の選択は正しかったか?** SQLite I/O がボトルネックなら別実装も検討。
5. **次に何を測りたいか?** Phase 1 のスコープを Phase 0 のデータで決定する。

---

## 1〜4 概要（簡易）

### Phase 1: 抽象化と階層
- Injector インターフェース化 (Process / Container)
- Observer / Recorder の分離
- リモート Recorder (別ホストへの JSONL stream)

### Phase 2: k8s 対応と data probe
- Pod / Node 階層 (client-go)
- 順序検証 probe (sequence number チェック)
- 重複検出
- NTP/PTP による時刻同期

### Phase 3: 4軸スコア化
- RT (Recovery Time)
- DI (Data Integrity)
- BR (Blast Radius)
- RC (Restart Cost)
- 重み感度分析

### Phase 4: 論文実験基盤
- レポート生成 (matplotlib via Python sidecar、または Go の gonum)
- 複数 SUT 横並び比較
- chaos injection の経験分布フィッティング
- arXiv論文 Figure 自動生成

---

## 次のアクション

1. このドキュメントを repository の `docs/PLAN.md` として配置
2. Phase 0 の git ブランチを切る (`phase0`)
3. Step 1-8 を Claude Code で順次実装
4. 最初の30 trial 実験を実行
5. データを見ながら本ドキュメントを更新 (実測値、修正点)

実装は Claude Code 環境で進める。本chatには Phase 0 完了後のデータを持ち込んで議論する。

