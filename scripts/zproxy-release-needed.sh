#!/bin/bash
# Does this release need a new zproxy? Prints, in environment-file form,
#   ZDXSV_SKIP_ZPROXY=true|false   (true: goreleaser skips the zproxy build, .goreleaser.yml)
#   ZDXSV_ZPROXY_PREV=<tag>        (the newest release that carries a zproxy archive)
# and the reasons on stderr.
#
# zproxy ships in a release only when its code changed since the last release that carried
# a zproxy archive. Otherwise every player's zproxy would self-update to an identical build
# (zproxy's self-update takes the newest release that has a zdxsv_<ver>_<os>_<arch>.zip and
# ignores the others). Compared: the in-repo packages zproxy depends on (go list -deps,
# tests excluded), go.mod, go.sum and .goreleaser.yml. With no previous zproxy release, or
# when the release list cannot be read, zproxy is built: that is always safe.
#
# Usage: scripts/zproxy-release-needed.sh [prev-tag]   # prev-tag skips the GitHub lookup
# Env: ZDXSV_REPO (default inada-s/zdxsv). The lookup uses gh when installed, else curl + jq
# (GITHUB_TOKEN optional).
set -u
cd "$(dirname "$0")/.."
repo=${ZDXSV_REPO:-inada-s/zdxsv}
prev=${1:-}

build() { # reason
	echo "zproxy: $1: building it" >&2
	echo "ZDXSV_SKIP_ZPROXY=false"
	echo "ZDXSV_ZPROXY_PREV=${prev:-none}"
	exit 0
}

if [ -z "$prev" ]; then
	# newest (the API lists newest first) non-draft, non-prerelease release with a zproxy archive
	q='[.[] | select(.draft == false and .prerelease == false)
	      | select(any(.assets[]; .name | test("^zdxsv_.*_(linux|darwin|windows)_[a-z0-9]+[.]zip$")))][0].tag_name // empty'
	if command -v gh > /dev/null; then
		prev=$(gh api "repos/$repo/releases?per_page=30" --jq "$q" 2>/dev/null) || prev=""
	else
		auth=()
		[ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
		prev=$(curl -fsS "${auth[@]}" "https://api.github.com/repos/$repo/releases?per_page=30" | jq -r "$q") || prev=""
	fi
	[ -n "$prev" ] || build "no previous release with a zproxy archive found (or the API call failed)"
fi
git rev-parse -q --verify "$prev^{commit}" > /dev/null || build "$prev is not in this checkout (shallow clone?)"

# zproxy's own package and the in-repo packages it imports, as paths (module zdxsv -> repo root)
pkgs=$(go list -deps -f '{{if .Module}}{{if eq .Module.Path "zdxsv"}}{{.ImportPath}}{{end}}{{end}}' ./src/zproxy | sed 's#^zdxsv/##')
[ -n "$pkgs" ] || build "go list found no packages"
# shellcheck disable=SC2086
changed=$(git diff --name-only "$prev" HEAD -- $pkgs go.mod go.sum .goreleaser.yml | grep -v '_test\.go$')
if [ -n "$changed" ]; then
	echo "zproxy: changed since $prev:" >&2
	echo "$changed" | sed 's/^/  /' >&2
	build "code changed"
fi
echo "zproxy: unchanged since $prev in $(echo $pkgs) go.mod go.sum .goreleaser.yml: not building it" >&2
echo "ZDXSV_SKIP_ZPROXY=true"
echo "ZDXSV_ZPROXY_PREV=$prev"
