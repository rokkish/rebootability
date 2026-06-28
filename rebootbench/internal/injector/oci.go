package injector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// runQuiet : exec.Command を走らせるが stdout/stderr を /dev/null へ捨てる。
//
// 理由: `crun run --detach` は double-fork した grandchild (= tinyserver) を
// 残す。Go の exec で Stdout/Stderr を nil のままにすると親の FD が継承され、
// 親が pipe (例: `| tail` や CombinedOutput) なら grandchild がその pipe FD
// を保持し続けて pipe が閉じず、parent / 全てのチェーンが永遠にブロックする。
// したがって stdout/stderr を明示的に /dev/null に切り替えてから crun を呼ぶ。
func runQuiet(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	return cmd.Run()
}

// OCIRestart : OCI runtime (runc / crun) を CLI ラッパーなしに直接叩く。
//
// 1 サイクル = `<runtime> delete --force <id>` (KILL + cgroup cleanup) →
// 任意の delay → `<runtime> run --bundle <bundle> --detach <id>`。
//
// container CLI (docker/podman) を介さないので、OCI runtime 単独のコストが
// 測れる。docker と podman は内部で同じ OCI runtime (crun/runc) を呼ぶので、
// この測定値が両者の下限値の意味を持つ。
type OCIRestart struct {
	Runtime string        // "runc" or "crun"
	ID      string        // container id (任意の識別子)
	Bundle  string        // OCI bundle のディレクトリ (絶対パス推奨)
	Delay   time.Duration // delete 後 run までの待ち時間
}

func NewOCIRestart(runtime, id, bundle string, delay time.Duration) *OCIRestart {
	return &OCIRestart{Runtime: runtime, ID: id, Bundle: bundle, Delay: delay}
}

func (o *OCIRestart) Mode() string { return o.Runtime + ":oci-restart" }

func (o *OCIRestart) Inject(ctx context.Context) (Event, error) {
	at := time.Now()
	if err := runQuiet(ctx, o.Runtime, "delete", "--force", o.ID); err != nil {
		return Event{InjectAt: at, Mode: o.Mode()}, fmt.Errorf("%s delete --force: %w", o.Runtime, err)
	}
	if o.Delay > 0 {
		select {
		case <-time.After(o.Delay):
		case <-ctx.Done():
			return Event{InjectAt: at, Mode: o.Mode()}, ctx.Err()
		}
	}
	startAt := time.Now()
	if err := runQuiet(ctx, o.Runtime, "run", "--bundle", o.Bundle, "--detach", o.ID); err != nil {
		return Event{InjectAt: at, StartAt: startAt, Mode: o.Mode()}, fmt.Errorf("%s run: %w", o.Runtime, err)
	}
	return Event{InjectAt: at, StartAt: startAt, Mode: o.Mode()}, nil
}
