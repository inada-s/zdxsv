#!/bin/bash
# Installs a zdxsv server release from GitHub into the zdxsv home (default /opt/zdxsv, as
# infra/systemd/zdxsv.service expects) and restarts the service when it is installed:
#   releases/<tag>/zdxsv (with zdxsv.service and the scripts), bin/zdxsv -> releases/<tag>/zdxsv,
#   bin/zdxsv-release.sh and bin/zdxsv-db.sh -> that release's scripts
# Usage: sudo zdxsv-release.sh [tag]      the latest release when no tag is given
#        sudo ZDXSV_NO_RESTART=1 zdxsv-release.sh [tag]   installs only (to migrate the database first)
# Run it as root (or as a user that owns ZDXSV_HOME): the service user must not be able to
# write its own binary. Needs curl, tar and sha256sum. The assets come from .github/workflows/release.yml.
set -euo pipefail
repo=inada-s/zdxsv
home=${ZDXSV_HOME:-/opt/zdxsv}
tag=${1:-}
mkdir -p "$home" 2>/dev/null || true
if [ ! -w "$home" ]; then
	echo "$home is not writable: run with sudo (or set ZDXSV_HOME)" >&2
	exit 1
fi
if [ -z "$tag" ]; then
	tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/^ *"tag_name": *"\([^"]*\)".*/\1/p')
	[ -n "$tag" ] || { echo "no latest release found" >&2; exit 1; }
fi
asset="zdxsv-server_${tag#v}_linux-x86_64.tar.gz"
url="https://github.com/$repo/releases/download/$tag"
dir="$home/releases/$tag"

if [ ! -x "$dir/zdxsv" ]; then
	mkdir -p "$dir"
	echo "downloading $url/$asset"
	curl -fsSL -o "$dir/$asset" "$url/$asset"
	curl -fsSL -o "$dir/checksums.txt" "$url/checksums.txt"
	(cd "$dir" && grep " $asset\$" checksums.txt | sha256sum -c -)
	tar --no-same-owner -xzf "$dir/$asset" -C "$dir"
	rm -f "$dir/$asset"
fi

mkdir -p "$home/bin"
ln -sfn "$dir/zdxsv" "$home/bin/zdxsv"
for f in zdxsv-release.sh zdxsv-db.sh; do
	if [ -e "$dir/$f" ]; then
		ln -sfn "$dir/$f" "$home/bin/$f"
	fi
done
echo "bin/zdxsv -> releases/$tag/zdxsv: $("$home/bin/zdxsv" version)"
if [ -z "${ZDXSV_NO_RESTART:-}" ] && systemctl is-enabled --quiet zdxsv 2>/dev/null; then
	if [ "$(id -u)" = 0 ]; then
		systemctl restart zdxsv
	else
		sudo systemctl restart zdxsv
	fi
	echo "zdxsv restarted"
fi
