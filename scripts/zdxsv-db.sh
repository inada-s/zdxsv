#!/bin/bash
# Runs `zdxsv <command>` (initdb, migratedb, ...) on the service's database: as the zdxsv user,
# with the service's environment (/etc/zdxsv/zdxsv.env) in its working directory (/var/lib/zdxsv).
# Usage: sudo zdxsv-db.sh <command> [args...]
# initdb and migratedb rewrite the tables, so they refuse to run while the service is active.
set -euo pipefail
[ $# -ge 1 ] || { echo "usage: sudo $0 <command> [args...]" >&2; exit 1; }
bin=${ZDXSV_HOME:-/opt/zdxsv}/bin/zdxsv
env=/etc/zdxsv/zdxsv.env
case $1 in
initdb | migratedb)
	if systemctl is-active --quiet zdxsv 2>/dev/null; then
		echo "zdxsv is running: sudo systemctl stop zdxsv first" >&2
		exit 1
	fi
	;;
esac
cd /var/lib/zdxsv
exec runuser -u zdxsv -- bash -c 'set -a; . "$1"; set +a; shift; exec "$@"' _ "$env" "$bin" "$@"
