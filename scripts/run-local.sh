#!/bin/bash
# Runs the whole server on this machine for local play: .env with this machine's LAN address as the
# public address, a database made if absent, TLS ports with the test certificate.
# Usage: [IP=<address>] scripts/run-local.sh [zdxsv flags]
# Emulator clients must trust the test CA (zdxsv/testdata), never use it on a public server.
set -eu
cd "$(dirname "$0")/.."

# The address of the default route's interface: the first non-loopback address can be a virtual
# switch (WSL, Hyper-V, Docker) that other machines cannot reach.
lan_ip() {
	case "$(uname -s)" in
	MINGW* | MSYS* | CYGWIN*)
		powershell -NoProfile -Command "\$r = Get-NetRoute -DestinationPrefix 0.0.0.0/0 | Sort-Object { \$_.RouteMetric + (Get-NetIPInterface -InterfaceIndex \$_.InterfaceIndex -AddressFamily IPv4).InterfaceMetric } | Select-Object -First 1; (Get-NetIPAddress -InterfaceIndex \$r.InterfaceIndex -AddressFamily IPv4 | Select-Object -First 1).IPAddress" | tr -d '\r'
		;;
	Darwin)
		ipconfig getifaddr "$(route -n get default | awk '/interface:/ {print $2}')"
		;;
	*)
		ip -4 route get 192.0.2.1 | sed -n 's/.* src \([0-9.]*\).*/\1/p'
		;;
	esac
}

IP=${IP:-$(lan_ip)}
if [ -z "$IP" ]; then
	echo "no LAN address found; set IP=<address>" >&2
	exit 2
fi

set -a
. ./.env
set +a
export ZDXSV_DNAS_PUBLIC_ADDR=$IP ZDXSV_LOGIN_PUBLIC_ADDR=$IP
export ZDXSV_LOBBY_PUBLIC_ADDR=$IP:8200 ZDXSV_BATTLE_PUBLIC_ADDR=$IP:8210
export ZDXSV_DNAS_DATA_DIR=${ZDXSV_DNAS_DATA_DIR:-$(scripts/dnasrep.sh)}
export ZDXSV_TLS_CERT_FILE=${ZDXSV_TLS_CERT_FILE:-zdxsv/testdata/server.crt}
export ZDXSV_TLS_KEY_FILE=${ZDXSV_TLS_KEY_FILE:-zdxsv/testdata/server.key}

bin=bin/zdxsv$(go env GOEXE)
mkdir -p bin
go build -o "$bin" ./zdxsv
[ -f "$ZDXSV_DB_NAME" ] || "$bin" initdb
echo "zdxsv on $IP (db $ZDXSV_DB_NAME)"
exec "$bin" -logtostderr "$@"
