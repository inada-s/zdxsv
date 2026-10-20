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
ZDXSV_LOBBY_API_ADDR=lobby:8203
ZDXSV_DB_NAME=zdxsv.db
EOS
fi
IP=$(sed -n 's/^ZDXSV_DNAS_PUBLIC_ADDR=//p' .env)

# the 5 zdxsv services share one image: build it once, or compose builds it 5 times in parallel
# (that stalled the CI runner until the job was killed)
docker build -q -f docker/zdxsv/Dockerfile . > /dev/null && $DC build -q || exit 1
# first deploy only: an empty file, else docker bind-mounts a directory
if [ ! -s zdxsv.db ]; then
	touch zdxsv.db
	$DC run --rm lobby initdb || exit 1
fi
$DC up -d || exit 1

for s in dns login lobby battle router dnas; do
	for i in $(seq 1 30); do
		$DC ps --status running --services | grep -qx $s && break
		sleep 2
	done
	check "running $s" sh -c "$DC ps --status running --services | grep -qx $s"
done

for h in gate1.jp.dnas.playstation.org www01.kddi-mmbb.jp ca1202.mmcp6 ca1203.mmcp6; do
	check "dns $h -> $IP" sh -c "[ \"\$(dig +short +time=2 +tries=3 @127.0.0.1 $h)\" = $IP ]"
done
for p in 443 8200 8201 8203 8210; do
	check "tcp $p open" timeout 3 bash -c "</dev/tcp/127.0.0.1/$p"
done
check "login health" $DC exec -T login wget -qO- http://127.0.0.1/health
check "dnas via router (DNAS answers, login proxy)" $DC run --rm --no-deps dns dnascheck router:443

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
