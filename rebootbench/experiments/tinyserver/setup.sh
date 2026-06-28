#!/usr/bin/env bash
# tinyserver を 4 層比較のために配置する一発スクリプト。
#
#   1. tinyserver を static build (CGO 無し)
#   2. systemd --user unit を ~/.config/systemd/user/ に配置
#   3. OCI bundle の rootfs に tinyserver を配置
#   4. docker と podman に rebootbench/tinyserver:latest を build
#
# 後は ../cooldown_sweep.sh や rebootbench --injector ... を呼べばよい。

set -euo pipefail
cd "$(dirname "$0")"

echo "== build tinyserver =="
CGO_ENABLED=0 go build -o tinyserver .

ABS_BIN="$(realpath tinyserver)"

echo "== install systemd --user unit =="
mkdir -p "$HOME/.config/systemd/user"
sed "s|ABSOLUTE_PATH_TO_TINYSERVER|$ABS_BIN|" rebootbench-tinyserver.service.tmpl \
  > "$HOME/.config/systemd/user/rebootbench-tinyserver.service"
systemctl --user daemon-reload
echo "  installed: ~/.config/systemd/user/rebootbench-tinyserver.service"
echo "  start with: systemctl --user start rebootbench-tinyserver.service"

echo "== populate OCI bundle rootfs =="
mkdir -p ../oci_bundle/rootfs
cp tinyserver ../oci_bundle/rootfs/tinyserver
echo "  copied to: ../oci_bundle/rootfs/tinyserver"
echo "  run with: crun run --bundle \$PWD/../oci_bundle --detach rebootbench-oci"

echo "== docker build =="
docker build -q -t rebootbench/tinyserver:latest . | sed 's/^/  /'

echo "== podman build =="
podman build -q -t rebootbench/tinyserver:latest . | sed 's/^/  /'

echo "done."
