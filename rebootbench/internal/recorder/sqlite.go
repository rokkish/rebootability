package recorder

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/rokkish/rebootbench/internal/observer"
)

const schema = `
CREATE TABLE IF NOT EXISTS experiment (
    id TEXT PRIMARY KEY,
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    container_name TEXT NOT NULL,
    probe_url TEXT NOT NULL,
    probe_interval_ns INTEGER NOT NULL,
    trial_count INTEGER NOT NULL,
    git_sha TEXT,
    notes TEXT
);

CREATE TABLE IF NOT EXISTS trial (
    experiment_id TEXT NOT NULL,
    trial_index INTEGER NOT NULL,
    injector_mode TEXT NOT NULL DEFAULT '',
    inject_at INTEGER NOT NULL,
    start_at INTEGER,
    first_recovery_at INTEGER,
    recovery_time_ns INTEGER,
    status TEXT NOT NULL,
    PRIMARY KEY (experiment_id, trial_index)
);
-- forward-compat: add columns when upgrading from Phase 0 schema
-- (SQLite ignores duplicate ADD COLUMN via error; we run them only if missing)

CREATE TABLE IF NOT EXISTS probe (
    experiment_id TEXT NOT NULL,
    trial_index INTEGER NOT NULL,
    sent_at INTEGER NOT NULL,
    latency_ns INTEGER,
    status_code INTEGER,
    error TEXT
);

CREATE INDEX IF NOT EXISTS idx_probe_experiment ON probe (experiment_id, trial_index, sent_at);
`

type Recorder struct {
	db          *sql.DB
	insertProbe *sql.Stmt
}

func Open(path string) (*Recorder, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema init: %w", err)
	}
	// migrate older Phase 0 DBs that don't have the new columns
	for _, alter := range []string{
		`ALTER TABLE trial ADD COLUMN injector_mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE trial ADD COLUMN start_at INTEGER`,
	} {
		if _, err := db.Exec(alter); err != nil {
			// "duplicate column name" means already applied
			if !strings.Contains(err.Error(), "duplicate column name") {
				db.Close()
				return nil, fmt.Errorf("migrate: %w (%s)", err, alter)
			}
		}
	}
	stmt, err := db.Prepare(`INSERT INTO probe (experiment_id, trial_index, sent_at, latency_ns, status_code, error) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Recorder{db: db, insertProbe: stmt}, nil
}

func (r *Recorder) Close() error {
	if r.insertProbe != nil {
		r.insertProbe.Close()
	}
	return r.db.Close()
}

type ExperimentRow struct {
	ID              string
	StartedAt       time.Time
	ContainerName   string
	ProbeURL        string
	ProbeInterval   time.Duration
	TrialCount      int
	GitSHA          string
	Notes           string
}

func (r *Recorder) SaveExperiment(e ExperimentRow) error {
	_, err := r.db.Exec(
		`INSERT INTO experiment (id, started_at, container_name, probe_url, probe_interval_ns, trial_count, git_sha, notes) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.StartedAt.UnixNano(), e.ContainerName, e.ProbeURL, int64(e.ProbeInterval), e.TrialCount, e.GitSHA, e.Notes,
	)
	return err
}

func (r *Recorder) FinishExperiment(id string, endedAt time.Time) error {
	_, err := r.db.Exec(`UPDATE experiment SET ended_at = ? WHERE id = ?`, endedAt.UnixNano(), id)
	return err
}

type TrialRow struct {
	ExperimentID    string
	Index           int
	InjectorMode    string
	InjectAt        time.Time
	StartAt         time.Time // zero -> NULL (only set in kill-start mode)
	FirstRecoveryAt time.Time // zero -> NULL
	RecoveryTime    time.Duration
	Status          string
}

func (r *Recorder) SaveTrial(t TrialRow) error {
	var startAt, firstRec, recNs interface{}
	if !t.StartAt.IsZero() {
		startAt = t.StartAt.UnixNano()
	}
	if !t.FirstRecoveryAt.IsZero() {
		firstRec = t.FirstRecoveryAt.UnixNano()
		recNs = int64(t.RecoveryTime)
	}
	_, err := r.db.Exec(
		`INSERT OR REPLACE INTO trial (experiment_id, trial_index, injector_mode, inject_at, start_at, first_recovery_at, recovery_time_ns, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ExperimentID, t.Index, t.InjectorMode, t.InjectAt.UnixNano(), startAt, firstRec, recNs, t.Status,
	)
	return err
}

func (r *Recorder) SaveProbe(experimentID string, trialIndex int, p observer.ProbeResult) error {
	var (
		latency    interface{}
		statusCode interface{}
		errStr     interface{}
	)
	if p.Err != nil {
		errStr = p.Err.Error()
	}
	if p.Latency > 0 {
		latency = int64(p.Latency)
	}
	if p.StatusCode != 0 {
		statusCode = p.StatusCode
	}
	_, err := r.insertProbe.Exec(experimentID, trialIndex, p.SentAt.UnixNano(), latency, statusCode, errStr)
	return err
}

// RecoveryTimes returns recovery_time_ns for all completed trials of an experiment, in trial_index order.
func (r *Recorder) RecoveryTimes(experimentID string) ([]int64, error) {
	rows, err := r.db.Query(
		`SELECT recovery_time_ns FROM trial WHERE experiment_id = ? AND status = 'completed' AND recovery_time_ns IS NOT NULL ORDER BY trial_index`,
		experimentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
