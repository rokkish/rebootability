package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/rokkish/rebootbench/internal/analyzer"
	"github.com/rokkish/rebootbench/internal/experiment"
	"github.com/rokkish/rebootbench/internal/injector"
	"github.com/rokkish/rebootbench/internal/recorder"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "phase0":
		runPhase0(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `rebootbench — measure recovery time of a containerized SUT

Usage:
  rebootbench phase0 [flags]

Run "rebootbench phase0 -h" for flags.
`)
}

func runPhase0(args []string) {
	fs := flag.NewFlagSet("phase0", flag.ExitOnError)
	container := fs.String("container", "rebootbench-nginx", "docker container name to kill")
	url := fs.String("url", "http://localhost:18080/", "probe URL")
	interval := fs.Duration("interval", 50*time.Millisecond, "probe interval")
	timeout := fs.Duration("probe-timeout", 30*time.Millisecond, "per-probe HTTP timeout")
	trials := fs.Int("trials", 30, "number of trials")
	preSettle := fs.Duration("pre-settle", 1*time.Second, "pre-inject settle window")
	postSettle := fs.Duration("post-settle", 1*time.Second, "post-recovery observation window")
	cooldown := fs.Duration("cooldown", 5*time.Second, "cooldown between trials")
	postTimeout := fs.Duration("recovery-timeout", 30*time.Second, "upper bound to wait for recovery")
	dbPath := fs.String("db", "rebootbench.db", "SQLite database path")
	csvPath := fs.String("csv", "", "optional path to write recovery times CSV (default <db_dir>/<experiment_id>.csv)")
	notes := fs.String("notes", "", "free-form notes saved with the experiment")
	gitSHA := fs.String("git-sha", "", "git SHA of rebootbench at run time (informational)")
	runtime := fs.String("runtime", "docker", "container runtime CLI: docker | podman")
	injectorMode := fs.String("injector", "kill-start",
		"injection mode: kill | kill-start | restart\n"+
			"  kill        : docker kill のみ。復活は SUT 環境に依存。\n"+
			"                自動復活がなければ status=no_recovery を記録する。\n"+
			"  kill-start  : docker kill 直後に外部から docker start を発行。\n"+
			"                突然死 + 外部観察者の即時再起動命令を測る。\n"+
			"  restart     : docker restart -t N (SIGTERM→grace→SIGKILL→start)。\n"+
			"                計画的再起動のコストを測る。")
	killStartDelay := fs.Duration("kill-start-delay", 0, "kill-start モードで kill 後 start までに入れる遅延")
	restartGrace := fs.Duration("restart-grace", 0, "restart モードの SIGTERM grace (docker restart -t)")
	_ = fs.Parse(args)

	rec, err := recorder.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer rec.Close()

	expID := uuid.NewString()
	expRow := recorder.ExperimentRow{
		ID:            expID,
		StartedAt:     time.Now(),
		ContainerName: *container,
		ProbeURL:      *url,
		ProbeInterval: *interval,
		TrialCount:    *trials,
		GitSHA:        *gitSHA,
		Notes:         *notes,
	}
	if err := rec.SaveExperiment(expRow); err != nil {
		log.Fatalf("save experiment: %v", err)
	}
	log.Printf("experiment %s started: runtime=%s container=%s url=%s trials=%d interval=%s injector=%s", expID, *runtime, *container, *url, *trials, *interval, *injectorMode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("signal received, cancelling experiment")
		cancel()
	}()

	var rt injector.Runtime
	switch *runtime {
	case "docker":
		rt = injector.RuntimeDocker
	case "podman":
		rt = injector.RuntimePodman
	default:
		log.Fatalf("unknown --runtime: %s (expected docker|podman)", *runtime)
	}
	var inj injector.Injector
	switch *injectorMode {
	case "kill":
		inj = injector.NewContainerKill(rt, *container)
	case "kill-start":
		inj = injector.NewContainerKillStart(rt, *container, *killStartDelay)
	case "restart":
		inj = injector.NewContainerRestart(rt, *container, *restartGrace)
	default:
		log.Fatalf("unknown --injector: %s", *injectorMode)
	}

	runner := &experiment.Runner{
		Cfg: experiment.Config{
			ContainerName: *container,
			ProbeURL:      *url,
			ProbeInterval: *interval,
			ProbeTimeout:  *timeout,
			TrialCount:    *trials,
			PreSettle:     *preSettle,
			PostSettle:    *postSettle,
			Cooldown:      *cooldown,
			PostTimeout:   *postTimeout,
		},
		ExperimentID: expID,
		Recorder:     rec,
		Injector:     inj,
	}

	for i := 0; i < *trials; i++ {
		if ctx.Err() != nil {
			break
		}
		log.Printf("trial %d/%d: starting", i+1, *trials)
		row, err := runner.RunTrial(ctx, i)
		if err != nil {
			log.Printf("trial %d: %v (status=%s)", i, err, row.Status)
		} else {
			log.Printf("trial %d: recovery=%s", i, row.RecoveryTime)
		}
		if i < *trials-1 {
			select {
			case <-time.After(*cooldown):
			case <-ctx.Done():
			}
		}
	}

	if err := rec.FinishExperiment(expID, time.Now()); err != nil {
		log.Printf("finish experiment: %v", err)
	}

	samples, err := rec.RecoveryTimes(expID)
	if err != nil {
		log.Fatalf("read recovery times: %v", err)
	}
	stats := analyzer.Compute(samples)
	fmt.Println()
	fmt.Printf("== Experiment %s ==\n", expID)
	fmt.Printf("container=%s url=%s trials_requested=%d\n", *container, *url, *trials)
	analyzer.PrintTable(os.Stdout, stats)

	if *csvPath == "" {
		*csvPath = filepath.Join(filepath.Dir(*dbPath), expID+".csv")
	}
	f, err := os.Create(*csvPath)
	if err != nil {
		log.Fatalf("create csv: %v", err)
	}
	defer f.Close()
	if err := analyzer.WriteCSV(f, samples); err != nil {
		log.Fatalf("write csv: %v", err)
	}
	fmt.Printf("CSV: %s\n", *csvPath)
	fmt.Printf("DB:  %s\n", *dbPath)
}
