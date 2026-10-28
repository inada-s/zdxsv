#!/bin/bash
# Smoke test of the server binary: builds bin/zdxsv (or takes $ZDXSV_BIN), runs it with .env
# (ports 53, or ZDXSV_DNS_ADDR's, and 443 need CAP_NET_BIND_SERVICE: `sudo setcap cap_net_bind_service=+ep bin/zdxsv`,
# or root) on a throwaway database, and checks each server from outside.
# Usage: IP=<public ip> scripts/smoke.sh   (default 127.0.0.1; writes .env only if absent)
set -u
cd "$(dirname "$0")/.."
IP=${IP:-127.0.0.1}
fails=0
check() { # name, command...
	local name=$1; shift
	if out=$("$@" 2>&1); then echo "PASS $name"; else echo "FAIL $name: $(echo "$out" | tail -3)"; fails=$((fails + 1)); fi
}

if [ ! -f .env ]; then
	cat > .env <<EOS
ZDXSV_DNAS_PUBLIC_ADDR=$IP
ZDXSV_LOGIN_PUBLIC_ADDR=$IP
ZDXSV_LOBBY_ADDR=:8200
ZDXSV_LOBBY_RPC_ADDR=:8201
ZDXSV_LOBBY_PUBLIC_ADDR=$IP:8200
ZDXSV_BATTLE_ADDR=:8210
ZDXSV_BATTLE_PUBLIC_ADDR=$IP:8210
ZDXSV_LOBBY_API_ADDR=:8203
ZDXSV_DB_NAME=zdxsv.db
EOS
fi
IP=$(sed -n 's/^ZDXSV_DNAS_PUBLIC_ADDR=//p' .env)

bin=${ZDXSV_BIN:-bin/zdxsv}
if [ -z "${ZDXSV_BIN:-}" ]; then
	mkdir -p bin && go build -o bin/zdxsv ./src/zdxsv || exit 1
fi
if command -v getcap >/dev/null && ! getcap "$bin" | grep -q net_bind_service; then
	echo "note: $bin has no cap_net_bind_service; ports 53 and 443 need it (or root)"
fi

# the server's environment: .env, but never the real database
set -a; . ./.env; set +a
export ZDXSV_DB_NAME=smoke.db
dns_addr=${ZDXSV_DNS_ADDR:-:53}
dns_port=${dns_addr##*:}
rm -f smoke.db
"$bin" initdb > /dev/null 2>&1 || exit 1
"$bin" > smoke.log 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null' EXIT

for i in $(seq 1 30); do
	curl -sf http://127.0.0.1:8203/lbs/status > /dev/null 2>&1 && break
	kill -0 $pid 2>/dev/null || break
	sleep 1
done
check "running" kill -0 $pid

for h in gate1.jp.dnas.playstation.org www01.kddi-mmbb.jp ca1202.mmcp6 ca1203.mmcp6; do
	check "dns :$dns_port $h -> $IP" sh -c "[ \"\$(dig +short +time=2 +tries=3 -p $dns_port @127.0.0.1 $h)\" = $IP ]"
done
for p in 443 8200 8201 8203 8210; do
	check "tcp $p open" timeout 3 bash -c "</dev/tcp/127.0.0.1/$p"
done
check "dnas on 443 (DNAS answers, login pages)" "$bin" -profile=0 dnascheck 127.0.0.1:443

stat=$(curl -sf http://127.0.0.1:8203/lbs/status)
echo "lbs/status: $stat"
check "lobby status api" sh -c "echo '$stat' | grep -q '\"Platforms\":{.*\"console\".*\"emu\"'"

if [ $fails -ne 0 ]; then
	tail -40 smoke.log
	echo "smoke: $fails FAIL"
	exit 1
fi
echo "smoke: all PASS"
