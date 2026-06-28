package injector

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"
)

type DockerKill struct {
	Container string
	Signal    string // e.g. "SIGKILL"; empty -> docker default (SIGKILL)
	// AutoStart: if true, fire `docker start` asynchronously right after kill.
	// On Docker 29.x we observed that `--restart=always` does NOT trigger a
	// restart after `docker kill`, so the external observer must request the
	// restart explicitly. Default true.
	AutoStart bool
}

func NewDockerKill(container string) *DockerKill {
	return &DockerKill{Container: container, AutoStart: true}
}

// Inject runs `docker kill <container>` and returns the wall-clock time
// immediately before the command was invoked. The timestamp is taken before
// exec so that it approximates "when the kill was requested" rather than
// "when the docker CLI returned".
func (d *DockerKill) Inject(ctx context.Context) (time.Time, error) {
	args := []string{"kill"}
	if d.Signal != "" {
		args = append(args, "-s", d.Signal)
	}
	args = append(args, d.Container)
	at := time.Now()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return at, fmt.Errorf("docker kill failed: %w: %s", err, string(out))
	}
	if d.AutoStart {
		go func(name string) {
			// detached from trial context: we want the start to happen even
			// if the trial loop moves on
			out, err := exec.Command("docker", "start", name).CombinedOutput()
			if err != nil {
				log.Printf("docker start %s: %v: %s", name, err, string(out))
			}
		}(d.Container)
	}
	return at, nil
}
