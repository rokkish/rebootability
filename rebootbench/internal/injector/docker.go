package injector

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// DockerKill : 純粋な kill。回復は SUT 環境 (restart policy / supervisor) 任せ。
// 何も再起動しなければ recovery は来ない — それが測定結果。
type DockerKill struct {
	Container string
	Signal    string // e.g. "SIGKILL"; empty -> docker default (SIGKILL)
}

func NewDockerKill(container string) *DockerKill {
	return &DockerKill{Container: container}
}

func (d *DockerKill) Mode() string { return "kill" }

func (d *DockerKill) Inject(ctx context.Context) (Event, error) {
	args := []string{"kill"}
	if d.Signal != "" {
		args = append(args, "-s", d.Signal)
	}
	args = append(args, d.Container)
	at := time.Now()
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: d.Mode()}, fmt.Errorf("docker kill: %w: %s", err, string(out))
	}
	return Event{InjectAt: at, Mode: d.Mode()}, nil
}

// DockerKillStart : kill 直後に (オプションで遅延を入れて) 外部から start を発行する。
// 「突然死 + 外部観察者が即時に再起動命令」というシナリオを測る。
// recovery_time = first_200 - inject_at は「kill から最初の 200 まで」だが、
// start_at も別途記録するので分解分析できる。
type DockerKillStart struct {
	Container string
	Signal    string
	Delay     time.Duration // kill 後に start を打つまでの待ち時間
}

func NewDockerKillStart(container string, delay time.Duration) *DockerKillStart {
	return &DockerKillStart{Container: container, Delay: delay}
}

func (d *DockerKillStart) Mode() string { return "kill-start" }

func (d *DockerKillStart) Inject(ctx context.Context) (Event, error) {
	args := []string{"kill"}
	if d.Signal != "" {
		args = append(args, "-s", d.Signal)
	}
	args = append(args, d.Container)
	at := time.Now()
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: d.Mode()}, fmt.Errorf("docker kill: %w: %s", err, string(out))
	}
	if d.Delay > 0 {
		select {
		case <-time.After(d.Delay):
		case <-ctx.Done():
			return Event{InjectAt: at, Mode: d.Mode()}, ctx.Err()
		}
	}
	startAt := time.Now()
	if out, err := exec.CommandContext(ctx, "docker", "start", d.Container).CombinedOutput(); err != nil {
		return Event{InjectAt: at, StartAt: startAt, Mode: d.Mode()},
			fmt.Errorf("docker start: %w: %s", err, string(out))
	}
	return Event{InjectAt: at, StartAt: startAt, Mode: d.Mode()}, nil
}

// DockerRestart : `docker restart -t N` を呼ぶ。SIGTERM → grace → SIGKILL → start を
// daemon が atomic に行う「計画的再起動」のコストを測る。
type DockerRestart struct {
	Container string
	StopGrace time.Duration // -t に渡す秒数。0 で即 SIGKILL
}

func NewDockerRestart(container string, stopGrace time.Duration) *DockerRestart {
	return &DockerRestart{Container: container, StopGrace: stopGrace}
}

func (d *DockerRestart) Mode() string { return "restart" }

func (d *DockerRestart) Inject(ctx context.Context) (Event, error) {
	secs := int(d.StopGrace.Seconds())
	if secs < 0 {
		secs = 0
	}
	at := time.Now()
	if out, err := exec.CommandContext(ctx, "docker", "restart", "-t", fmt.Sprintf("%d", secs), d.Container).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: d.Mode()}, fmt.Errorf("docker restart: %w: %s", err, string(out))
	}
	return Event{InjectAt: at, Mode: d.Mode()}, nil
}
