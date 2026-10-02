#!/bin/bash
# Smoke test of the production compose file (docker-compose.yml only, no dev override).
# Brings the stack up on this host and checks each service from outside and inside.
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
ZDXSV_LOGIN_ADDR=login:80
ZDXSV_LOGIN_PUBLIC_ADDR=$IP
ZDXSV_LOBBY_ADDR=lobby:8200
ZDXSV_LOBBY_RPC_ADDR=lobby:8201
ZDXSV_LOBBY_PUBLIC_ADDR=$IP:8200
ZDXSV_BATTLE_ADDR=battle:8210
ZDXSV_BATTLE_RPC_ADDR=battle:3080
ZDXSV_BATTLE_PUBLIC_ADDR=$IP:8210
ZDXSV_STATUS_ADDR=status:8080
ZDXSV_DB_NAME=zdxsv.db
EOS
fi
IP=$(sed -n 's/^ZDXSV_DNAS_PUBLIC_ADDR=//p' .env)

# first deploy only: an empty file, else docker bind-mounts a directory
if [ ! -s zdxsv.db ]; then
	touch zdxsv.db
	$DC run --rm lobby initdb || exit 1
fi
$DC up -d --build || exit 1

for s in dns login lobby battle status router legacyweb web; do
	for i in $(seq 1 30); do
		$DC ps --status running --services | grep -qx $s && break
		sleep 2
	done
	check "running $s" sh -c "$DC ps --status running --services | grep -qx $s"
done

for h in gate1.jp.dnas.playstation.org www01.kddi-mmbb.jp ca1202.mmcp6 ca1203.mmcp6; do
	check "dns $h -> $IP" sh -c "[ \"\$(dig +short +time=2 +tries=3 @127.0.0.1 $h)\" = $IP ]"
done
for p in 443 8200 8201 8210; do
	check "tcp $p open" timeout 3 bash -c "</dev/tcp/127.0.0.1/$p"
done
check "login health (web -> login)" $DC exec -T web curl -sf http://login/health
check "dnas proxy (legacyweb -> login)" $DC exec -T legacyweb wget -qO- --no-check-certificate https://localhost/00000020/health

sleep 12 # status polls the lobby every 10 s
stat=$($DC exec -T web curl -sf http://localhost/api/stat)
echo "api/stat: $stat"
check "status api via nginx" sh -c "echo '$stat' | grep -q '\"Platforms\":{.*\"console\".*\"emu-x86/64\"'"
check "status polled the lobby" sh -c "! $DC logs status 2>&1 | grep -q '^status.*E[0-9]\{4\} '"

if [ $fails -ne 0 ]; then
	$DC ps -a
	$DC logs --tail 30
	echo "smoke: $fails FAIL"
	exit 1
fi
echo "smoke: all PASS"
