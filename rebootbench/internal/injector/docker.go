package injector

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

type DockerKill struct {
	Container string
	Signal    string // e.g. "SIGKILL"; empty -> docker default (SIGKILL)
}

func NewDockerKill(container string) *DockerKill {
	return &DockerKill{Container: container}
}

// Inject runs `docker kill <container>` and returns the wall-clock time
// immediately before the command was invoked. We take the timestamp before
// exec so that the value approximates "when the kill was requested" rather
// than "when the docker CLI returned".
func (d *DockerKill) Inject(ctx context.Context) (time.Time, error) {
	args := []string{"kill"}
	if d.Signal != "" {
		args = append(args, "-s", d.Signal)
	}
	args = append(args, d.Container)
	at := time.Now()
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return at, fmt.Errorf("docker kill failed: %w: %s", err, string(out))
	}
	return at, nil
}
