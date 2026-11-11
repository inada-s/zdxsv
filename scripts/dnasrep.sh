#!/bin/bash
# Fetches DNASrep (github.com/FogNo23/DNASrep: the DNAS certificates and canned answers,
# which this repository does not carry) at the commit zdxsv is tested against, and prints
# the directory to use as ZDXSV_DNAS_DATA_DIR.
# Usage: scripts/dnasrep.sh [dir]   (default dnasrep/)
set -euo pipefail
dir=${1:-dnasrep}
commit=8e8dad3cae5b340ee1b15157d2db8181f18526e0
if [ ! -d "$dir/.git" ]; then
	git clone -q https://github.com/FogNo23/DNASrep "$dir" >&2
fi
git -C "$dir" -c advice.detachedHead=false checkout -q "$commit" >&2
cd "$dir" && pwd
