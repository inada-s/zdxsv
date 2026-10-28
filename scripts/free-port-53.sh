#!/bin/bash
# CI only: frees UDP port 53 on a hosted Ubuntu runner for scripts/smoke.sh.
# systemd-resolved's stub holds 127.0.0.53:53 (and 127.0.0.54:53); drop only the stub
# (stopping resolved cut the runner off). The drop-in sorts last so it beats the image's own.
# Waits until nothing listens on :53, else names the holder and fails here, not in the smoke.
set -eu
sudo mkdir -p /etc/systemd/resolved.conf.d
printf '[Resolve]\nDNSStubListener=no\n' | sudo tee /etc/systemd/resolved.conf.d/zz-zdxsv-nostub.conf
sudo ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf
sudo systemctl restart systemd-resolved
getent hosts github.com
for i in $(seq 1 20); do
	[ -z "$(ss -Hlun 'sport = :53')" ] && exit 0
	sleep 0.5
done
echo "port 53 still held:"
sudo ss -lunp 'sport = :53'
systemd-analyze cat-config systemd/resolved.conf | grep -v '^#' | grep .
exit 1
