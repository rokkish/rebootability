package injector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// K8sDeletePod : kubectl で pod を delete し、Deployment/ReplicaSet による
// 自動再作成 + Service の endpoint 反映までを 1 サイクルとして測る。
//
// Namespace と LabelSelector で対象 pod を 1 つ選ぶ (replicas=1 想定)。
// grace=0 (force) で SIGKILL 相当。
//
// 「pod を kill した時刻 = inject_at」とする。新 pod の名前は systemd 的に
// auto-recovery されるので start_at は使わない (Event.StartAt はゼロ)。
type K8sDeletePod struct {
	Namespace     string
	LabelSelector string // 例: "app=rebootbench-tiny"
	GracePeriod   int    // seconds. 0 = force
	Kubectl       string // default: "kubectl"
}

func NewK8sDeletePod(ns, selector string) *K8sDeletePod {
	return &K8sDeletePod{
		Namespace:     ns,
		LabelSelector: selector,
		GracePeriod:   0,
		Kubectl:       "kubectl",
	}
}

func (k *K8sDeletePod) Mode() string { return "k8s:delete-pod" }

func (k *K8sDeletePod) Inject(ctx context.Context) (Event, error) {
	// まず削除対象の pod 名を確定する。これは kubectl が wait しないコマンドで
	// 早く返るので inject_at の精度を保てる (pod を get で名前解決した後で
	// 改めて delete を打つ形)。
	getCmd := exec.CommandContext(ctx, k.Kubectl,
		"-n", k.Namespace,
		"get", "pod", "-l", k.LabelSelector,
		"-o", "jsonpath={.items[0].metadata.name}")
	getOut, err := getCmd.Output()
	if err != nil {
		return Event{Mode: k.Mode()}, fmt.Errorf("kubectl get pod: %w", err)
	}
	podName := strings.TrimSpace(string(getOut))
	if podName == "" {
		return Event{Mode: k.Mode()}, fmt.Errorf("no pod matched selector %q in ns %q", k.LabelSelector, k.Namespace)
	}

	args := []string{
		"-n", k.Namespace,
		"delete", "pod", podName,
		"--grace-period", fmt.Sprintf("%d", k.GracePeriod),
		"--wait=false",
	}
	if k.GracePeriod == 0 {
		args = append(args, "--force")
	}

	at := time.Now()
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return Event{InjectAt: at, Mode: k.Mode()}, err
	}
	defer devnull.Close()
	cmd := exec.CommandContext(ctx, k.Kubectl, args...)
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	if err := cmd.Run(); err != nil {
		return Event{InjectAt: at, Mode: k.Mode()}, fmt.Errorf("kubectl delete pod %s: %w", podName, err)
	}
	return Event{InjectAt: at, Mode: k.Mode()}, nil
}
