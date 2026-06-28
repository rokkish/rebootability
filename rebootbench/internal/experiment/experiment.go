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
	Injector     *injector.DockerKill
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
	if !gotPreOK {
		row := recorder.TrialRow{ExperimentID: r.ExperimentID, Index: idx, InjectAt: time.Now(), Status: "pre_settle_failed"}
		_ = r.Recorder.SaveTrial(row)
		return row, fmt.Errorf("trial %d: pre-settle saw no 200", idx)
	}

	// 2. inject
	injectAt, err := r.Injector.Inject(ctx)
	if err != nil {
		row := recorder.TrialRow{ExperimentID: r.ExperimentID, Index: idx, InjectAt: injectAt, Status: "inject_failed"}
		_ = r.Recorder.SaveTrial(row)
		return row, err
	}

	// 3. wait for recovery: first 200 with SentAt >= injectAt
	recoveryDeadline := injectAt.Add(r.Cfg.PostTimeout)
	var firstRecoveryAt time.Time
	for firstRecoveryAt.IsZero() {
		if time.Now().After(recoveryDeadline) {
			row := recorder.TrialRow{ExperimentID: r.ExperimentID, Index: idx, InjectAt: injectAt, Status: "timeout"}
			_ = r.Recorder.SaveTrial(row)
			return row, fmt.Errorf("trial %d: recovery timeout", idx)
		}
		select {
		case <-ctx.Done():
			return recorder.TrialRow{}, ctx.Err()
		case p, ok := <-probeCh:
			if !ok {
				return recorder.TrialRow{}, fmt.Errorf("probe channel closed during recovery wait")
			}
			saveAndCheck(p)
			if p.Err == nil && p.StatusCode == 200 && !p.SentAt.Before(injectAt) {
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
		InjectAt:        injectAt,
		FirstRecoveryAt: firstRecoveryAt,
		RecoveryTime:    firstRecoveryAt.Sub(injectAt),
		Status:          "completed",
	}
	if err := r.Recorder.SaveTrial(row); err != nil {
		return row, fmt.Errorf("save trial: %w", err)
	}
	return row, nil
}
