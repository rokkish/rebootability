-- Phase 0.5 cooldown sweep の集計
-- usage: sqlite3 rebootbench.db < experiments/sweep_summary.sql

.mode column
.headers on

-- 1. cooldown ごとの完走率
SELECT
  e.notes AS sweep,
  e.id AS exp,
  e.trial_count AS requested,
  COUNT(t.trial_index) AS recorded,
  SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END) AS completed,
  printf('%.1f%%', 100.0 * SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END) / e.trial_count) AS pct
FROM experiment e
LEFT JOIN trial t ON t.experiment_id = e.id
WHERE e.notes LIKE 'Phase 0.5%'
GROUP BY e.id
ORDER BY e.started_at;

-- 2. 完走 trial の recovery time 統計
.print
.print -- recovery time stats (completed only) --
SELECT
  e.notes AS sweep,
  COUNT(t.trial_index) AS n,
  printf('%.0f', MIN(t.recovery_time_ns)/1e6)  AS min_ms,
  printf('%.0f', AVG(t.recovery_time_ns)/1e6)  AS avg_ms,
  printf('%.0f', MAX(t.recovery_time_ns)/1e6)  AS max_ms
FROM experiment e
JOIN trial t ON t.experiment_id = e.id AND t.status = 'completed'
WHERE e.notes LIKE 'Phase 0.5%'
GROUP BY e.id
ORDER BY e.started_at;
