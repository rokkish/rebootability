package injector

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// SystemctlRestart : systemd 管理下のユニットを `systemctl restart <unit>` で
// 再起動する。bare process (コンテナなし) の下限値を測るのに使う。
// `--user` を指定すると user instance を叩く。
type SystemctlRestart struct {
	Unit string
	User bool // true -> systemctl --user
}

func NewSystemctlRestart(unit string, user bool) *SystemctlRestart {
	return &SystemctlRestart{Unit: unit, User: user}
}

func (s *SystemctlRestart) Mode() string {
	if s.User {
		return "systemctl-user:restart"
	}
	return "systemctl:restart"
}

func (s *SystemctlRestart) systemctlArgs(sub string) []string {
	args := []string{}
	if s.User {
		args = append(args, "--user")
	}
	args = append(args, sub, s.Unit)
	return args
}

func (s *SystemctlRestart) Inject(ctx context.Context) (Event, error) {
	args := s.systemctlArgs("restart")
	at := time.Now()
	if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: s.Mode()}, fmt.Errorf("systemctl restart: %w: %s", err, string(out))
	}
	return Event{InjectAt: at, Mode: s.Mode()}, nil
}

// SystemctlKill : `systemctl kill --signal=SIGKILL <unit>`。
// unit の Restart= 設定に従って systemd が再起動する。
type SystemctlKill struct {
	Unit   string
	User   bool
	Signal string // default SIGKILL
}

func NewSystemctlKill(unit string, user bool) *SystemctlKill {
	return &SystemctlKill{Unit: unit, User: user, Signal: "SIGKILL"}
}

func (s *SystemctlKill) Mode() string {
	if s.User {
		return "systemctl-user:kill"
	}
	return "systemctl:kill"
}

func (s *SystemctlKill) Inject(ctx context.Context) (Event, error) {
	args := []string{}
	if s.User {
		args = append(args, "--user")
	}
	sig := s.Signal
	if sig == "" {
		sig = "SIGKILL"
	}
	args = append(args, "kill", "-s", sig, s.Unit)
	at := time.Now()
	if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil {
		return Event{InjectAt: at, Mode: s.Mode()}, fmt.Errorf("systemctl kill: %w: %s", err, string(out))
	}
	return Event{InjectAt: at, Mode: s.Mode()}, nil
}
