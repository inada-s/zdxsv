#!/bin/bash
# Installs a zdxsv server release from GitHub into the zdxsv home (default ~/zdxsv, the
# service's WorkingDirectory) and restarts the service when it is installed:
#   releases/<tag>/zdxsv (with zdxsv.service and this script), bin/zdxsv -> releases/<tag>/zdxsv
# Usage: zdxsv-release.sh [tag]      the latest release when no tag is given
# Needs curl, tar and sha256sum. The assets come from .github/workflows/release.yml.
set -euo pipefail
repo=inada-s/zdxsv
home=${ZDXSV_HOME:-$HOME/zdxsv}
tag=${1:-}
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
	tar -xzf "$dir/$asset" -C "$dir"
	rm -f "$dir/$asset"
fi

mkdir -p "$home/bin"
ln -sfn "$dir/zdxsv" "$home/bin/zdxsv"
echo "bin/zdxsv -> releases/$tag/zdxsv: $("$home/bin/zdxsv" version)"
if systemctl is-enabled --quiet zdxsv 2>/dev/null; then
	sudo systemctl restart zdxsv && echo "zdxsv restarted"
fi
