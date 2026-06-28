package injector

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Runtime はコンテナ CLI の名前 ("docker" / "podman" など)。
// docker と podman は CLI 互換 (kill / start / restart のサブコマンドが同義)
// なので、ここでは 1 つの実装をパラメタライズして使い回す。
type Runtime string

const (
	RuntimeDocker Runtime = "docker"
	RuntimePodman Runtime = "podman"
)

// ContainerKill : 純粋な kill。回復は SUT 環境 (restart policy / supervisor) 任せ。
type ContainerKill struct {
	Runtime   Runtime
	Container string
	Signal    string
}

func NewContainerKill(rt Runtime, container string) *ContainerKill {
	return &ContainerKill{Runtime: rt, Container: container}
}

func (d *ContainerKill) Mode() string { return string(d.Runtime) + ":kill" }

func (d *ContainerKill) Inject(ctx context.Context) (Event, error) {
	args := []string{"kill"}
	if d.Signal != "" {
		args = append(args, "-s", d.Signal)
	}
	args = append(args, d.Container)
	at := time.Now()
	if out, err := exec.CommandContext(ctx, string(d.Runtime), args...).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: d.Mode()}, fmt.Errorf("%s kill: %w: %s", d.Runtime, err, string(out))
	}
	return Event{InjectAt: at, Mode: d.Mode()}, nil
}

// ContainerKillStart : kill 直後に (任意の遅延を入れて) 外部から start を発行。
type ContainerKillStart struct {
	Runtime   Runtime
	Container string
	Signal    string
	Delay     time.Duration
}

func NewContainerKillStart(rt Runtime, container string, delay time.Duration) *ContainerKillStart {
	return &ContainerKillStart{Runtime: rt, Container: container, Delay: delay}
}

func (d *ContainerKillStart) Mode() string { return string(d.Runtime) + ":kill-start" }

func (d *ContainerKillStart) Inject(ctx context.Context) (Event, error) {
	args := []string{"kill"}
	if d.Signal != "" {
		args = append(args, "-s", d.Signal)
	}
	args = append(args, d.Container)
	at := time.Now()
	if out, err := exec.CommandContext(ctx, string(d.Runtime), args...).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: d.Mode()}, fmt.Errorf("%s kill: %w: %s", d.Runtime, err, string(out))
	}
	if d.Delay > 0 {
		select {
		case <-time.After(d.Delay):
		case <-ctx.Done():
			return Event{InjectAt: at, Mode: d.Mode()}, ctx.Err()
		}
	}
	startAt := time.Now()
	if out, err := exec.CommandContext(ctx, string(d.Runtime), "start", d.Container).CombinedOutput(); err != nil {
		return Event{InjectAt: at, StartAt: startAt, Mode: d.Mode()},
			fmt.Errorf("%s start: %w: %s", d.Runtime, err, string(out))
	}
	return Event{InjectAt: at, StartAt: startAt, Mode: d.Mode()}, nil
}

// ContainerRestart : `<runtime> restart -t N` を呼ぶ。
type ContainerRestart struct {
	Runtime   Runtime
	Container string
	StopGrace time.Duration
}

func NewContainerRestart(rt Runtime, container string, stopGrace time.Duration) *ContainerRestart {
	return &ContainerRestart{Runtime: rt, Container: container, StopGrace: stopGrace}
}

func (d *ContainerRestart) Mode() string { return string(d.Runtime) + ":restart" }

func (d *ContainerRestart) Inject(ctx context.Context) (Event, error) {
	secs := int(d.StopGrace.Seconds())
	if secs < 0 {
		secs = 0
	}
	at := time.Now()
	if out, err := exec.CommandContext(ctx, string(d.Runtime), "restart", "-t", fmt.Sprintf("%d", secs), d.Container).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: d.Mode()}, fmt.Errorf("%s restart: %w: %s", d.Runtime, err, string(out))
	}
	return Event{InjectAt: at, Mode: d.Mode()}, nil
}
