#!/bin/bash
# Smoke test of the production compose file (docker-compose.yml only, no dev override).
# Brings the stack (one container, `zdxsv all`) up on this host and checks each server.
# Usage: IP=<public ip> docker/smoke.sh   (default 127.0.0.1; writes .env only if absent)
set -u
cd "$(dirname "$0")/.."
IP=${IP:-127.0.0.1}
DC="docker compose -f docker-compose.yml"
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

$DC build -q || exit 1
# first deploy only: an empty file, else docker bind-mounts a directory
if [ ! -s zdxsv.db ]; then
	touch zdxsv.db
	$DC run --rm zdxsv initdb || exit 1
fi
$DC up -d || exit 1

for i in $(seq 1 30); do
	$DC ps --status running --services | grep -qx zdxsv && break
	sleep 2
done
check "running zdxsv" sh -c "$DC ps --status running --services | grep -qx zdxsv"

for h in gate1.jp.dnas.playstation.org www01.kddi-mmbb.jp ca1202.mmcp6 ca1203.mmcp6; do
	check "dns $h -> $IP" sh -c "[ \"\$(dig +short +time=2 +tries=3 @127.0.0.1 $h)\" = $IP ]"
done
for p in 443 8200 8201 8203 8210; do
	check "tcp $p open" timeout 3 bash -c "</dev/tcp/127.0.0.1/$p"
done
check "dnas on 443 (DNAS answers, login pages)" $DC exec -T zdxsv /zdxsv dnascheck 127.0.0.1:443

stat=$(curl -sf http://127.0.0.1:8203/lbs/status)
echo "lbs/status: $stat"
check "lobby status api" sh -c "echo '$stat' | grep -q '\"Platforms\":{.*\"console\".*\"emu-x86/64\"'"

if [ $fails -ne 0 ]; then
	$DC ps -a
	$DC logs --tail 30
	echo "smoke: $fails FAIL"
	exit 1
fi
echo "smoke: all PASS"
