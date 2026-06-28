package experiment

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/rokkish/rebootbench/internal/injector"
	"github.com/rokkish/rebootbench/internal/observer"
	"github.com/rokkish/rebootbench/internal/recorder"
)

type Config struct {
	ContainerName string
	ProbeURL      string
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	TrialCount    int
	PreSettle     time.Duration
	PostSettle    time.Duration
	Cooldown      time.Duration
	PostTimeout   time.Duration // upper bound for recovery wait
}

type Runner struct {
	Cfg          Config
	ExperimentID string
	Recorder     *recorder.Recorder
	Injector     injector.Injector
}

// RunTrial executes one inject + observe cycle and persists results incrementally.
// Returns the TrialRow that was saved.
func (r *Runner) RunTrial(ctx context.Context, idx int) (recorder.TrialRow, error) {
	probe := observer.NewHTTPProbe(r.Cfg.ProbeURL, r.Cfg.ProbeInterval, r.Cfg.ProbeTimeout)

	trialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	probeCh := probe.Start(trialCtx)

	// drain helper: persist every probe as it arrives, optionally test predicate
	saveAndCheck := func(p observer.ProbeResult) {
		if err := r.Recorder.SaveProbe(r.ExperimentID, idx, p); err != nil {
			log.Printf("trial %d: save probe: %v", idx, err)
		}
	}

	// 1. pre-settle: require at least one 200 before injecting
	preDeadline := time.Now().Add(r.Cfg.PreSettle)
	gotPreOK := false
	for time.Now().Before(preDeadline) {
		select {
		case <-ctx.Done():
			return recorder.TrialRow{}, ctx.Err()
		case p, ok := <-probeCh:
			if !ok {
				return recorder.TrialRow{}, fmt.Errorf("probe channel closed before pre-settle")
			}
			saveAndCheck(p)
			if p.Err == nil && p.StatusCode == 200 {
				gotPreOK = true
			}
		}
	}
	mode := r.Injector.Mode()
	if !gotPreOK {
		row := recorder.TrialRow{ExperimentID: r.ExperimentID, Index: idx, InjectorMode: mode, InjectAt: time.Now(), Status: "pre_settle_failed"}
		_ = r.Recorder.SaveTrial(row)
		return row, fmt.Errorf("trial %d: pre-settle saw no 200", idx)
	}

	// 2. inject
	ev, err := r.Injector.Inject(ctx)
	if err != nil {
		row := recorder.TrialRow{ExperimentID: r.ExperimentID, Index: idx, InjectorMode: mode, InjectAt: ev.InjectAt, StartAt: ev.StartAt, Status: "inject_failed"}
		_ = r.Recorder.SaveTrial(row)
		return row, err
	}
	injectAt := ev.InjectAt

	// 3. wait for recovery.
	// 注意: 「inject 後に最初の 200」だけだと、`docker kill` CLI が返るまでの
	// 数百ms間に SUT がまだ生きていて 200 を返してしまうため、それを誤って
	// recovery と判定する。これを防ぐため、recovery = "inject 後に少なくとも
	// 1 回失敗 (err or non-2xx) を観測した後の、最初の 200" と定義する。
	recoveryDeadline := injectAt.Add(r.Cfg.PostTimeout)
	var firstRecoveryAt time.Time
	sawFailure := false
	for firstRecoveryAt.IsZero() {
		if time.Now().After(recoveryDeadline) {
			status := "timeout"
			// kill モードで自動復活がない環境では、これは「環境の自己復活力ゼロ」という
			// 計測結果。後段の分析が区別できるよう専用ステータスを残す。
			if mode == "kill" {
				status = "no_recovery"
			}
			row := recorder.TrialRow{ExperimentID: r.ExperimentID, Index: idx, InjectorMode: mode, InjectAt: injectAt, StartAt: ev.StartAt, Status: status}
			_ = r.Recorder.SaveTrial(row)
			return row, fmt.Errorf("trial %d: %s", idx, status)
		}
		select {
		case <-ctx.Done():
			return recorder.TrialRow{}, ctx.Err()
		case p, ok := <-probeCh:
			if !ok {
				return recorder.TrialRow{}, fmt.Errorf("probe channel closed during recovery wait")
			}
			saveAndCheck(p)
			if p.SentAt.Before(injectAt) {
				continue
			}
			if p.Err != nil || p.StatusCode != 200 {
				sawFailure = true
				continue
			}
			if sawFailure {
				firstRecoveryAt = p.SentAt
			}
		}
	}

	// 4. post-settle: keep probing for a bit to confirm stability and capture latency
	postDeadline := time.Now().Add(r.Cfg.PostSettle)
	for time.Now().Before(postDeadline) {
		select {
		case <-ctx.Done():
			return recorder.TrialRow{}, ctx.Err()
		case p, ok := <-probeCh:
			if !ok {
				break
			}
			saveAndCheck(p)
		}
	}

	row := recorder.TrialRow{
		ExperimentID:    r.ExperimentID,
		Index:           idx,
		InjectorMode:    mode,
		InjectAt:        injectAt,
		StartAt:         ev.StartAt,
		FirstRecoveryAt: firstRecoveryAt,
		RecoveryTime:    firstRecoveryAt.Sub(injectAt),
		Status:          "completed",
	}
	if err := r.Recorder.SaveTrial(row); err != nil {
		return row, fmt.Errorf("save trial: %w", err)
	}
	return row, nil
}
