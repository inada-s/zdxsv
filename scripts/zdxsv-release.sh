#!/bin/bash
# Installs a zdxsv server release from GitHub into the zdxsv home (default /opt/zdxsv, as
# infra/systemd/zdxsv.service expects): releases/<tag>/zdxsv, bin/zdxsv -> releases/<tag>/zdxsv.
# Nothing happens when <tag> is already the installed one. Otherwise a running service is stopped,
# an upgrade migrates the database (copied first to <db>.pre-update) with zdxsv-db.sh, and the
# service is started again; a failed migration puts the previous release back.
# Usage: sudo zdxsv-release.sh [tag]      the latest release when no tag is given
# ZDXSV_NO_RESTART=1: never stop or start the service (zdxsv.service runs it so before it starts).
# Run it as root: the service user must not be able to write its own binary.
# Needs curl, tar, sha256sum and sort -V. The assets come from .github/workflows/release.yml.
set -euo pipefail
repo=inada-s/zdxsv
home=${ZDXSV_HOME:-/opt/zdxsv}
envfile=${ZDXSV_ENV_FILE:-/etc/zdxsv/zdxsv.env}
tag=${1:-}
curl=(curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors --max-time 120)
mkdir -p "$home" 2>/dev/null || true
if [ ! -w "$home" ]; then
	echo "$home is not writable: run with sudo (or set ZDXSV_HOME)" >&2
	exit 1
fi
if [ -z "$tag" ]; then
	tag=$("${curl[@]}" "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/^ *"tag_name": *"\([^"]*\)".*/\1/p')
	[ -n "$tag" ] || { echo "no latest release found" >&2; exit 1; }
fi
current=$(basename "$(dirname "$(readlink "$home/bin/zdxsv" || echo none/zdxsv)")")
if [ "$current" = "$tag" ]; then
	echo "zdxsv $tag is installed"
	exit 0
fi
asset="zdxsv-server_${tag#v}_linux-x86_64.tar.gz"
url="https://github.com/$repo/releases/download/$tag"
dir="$home/releases/$tag"

if [ ! -x "$dir/zdxsv" ]; then
	mkdir -p "$dir"
	echo "downloading $url/$asset"
	"${curl[@]}" -o "$dir/$asset" "$url/$asset"
	"${curl[@]}" -o "$dir/checksums.txt" "$url/checksums.txt"
	(cd "$dir" && grep " $asset\$" checksums.txt | sha256sum -c -)
	tar --no-same-owner -xzf "$dir/$asset" -C "$dir"
	rm -f "$dir/$asset"
fi

systemctl_() {
	if [ "$(id -u)" = 0 ]; then systemctl "$@"; else sudo systemctl "$@"; fi
}
running=
if [ -z "${ZDXSV_NO_RESTART:-}" ] && systemctl is-active --quiet zdxsv 2>/dev/null; then
	running=1
	systemctl_ stop zdxsv
fi
mkdir -p "$home/bin"
ln -sfn "$dir/zdxsv" "$home/bin/zdxsv"
echo "bin/zdxsv -> releases/$tag/zdxsv (was $current)"

db=$(sed -n 's/^ZDXSV_DB_NAME=//p' "$envfile" 2>/dev/null || true)
case $db in
"" | /*) ;;
*) db=/var/lib/zdxsv/$db ;;
esac
newest=$(printf '%s\n' "$current" "$tag" | sort -V | tail -1)
if [ "$current" != none ] && [ "$newest" = "$tag" ] && [ -f "$db" ]; then
	cp -p "$db" "$db.pre-update"
	log=$(ZDXSV_HOME=$home bash "$(dirname "$0")/zdxsv-db.sh" migratedb 2>&1) || true
	echo "$log"
	if ! grep -q "Migration done" <<<"$log"; then
		echo "migratedb failed: back to $current; $db.pre-update holds the database before it" >&2
		ln -sfn "$home/releases/$current/zdxsv" "$home/bin/zdxsv"
		if [ -n "$running" ]; then systemctl_ start zdxsv; fi
		exit 1
	fi
fi
if [ -n "$running" ]; then
	systemctl_ start zdxsv
	echo "zdxsv restarted"
fi
