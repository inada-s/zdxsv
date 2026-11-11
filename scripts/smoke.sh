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
ZDXSV_LOBBY_ZPROXY_STUN_ADDR=:8201
ZDXSV_LOBBY_PUBLIC_ADDR=$IP:8200
ZDXSV_BATTLE_ADDR=:8210
ZDXSV_BATTLE_PUBLIC_ADDR=$IP:8210
ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203
ZDXSV_DB_NAME=zdxsv.db
EOS
fi
IP=$(sed -n 's/^ZDXSV_DNAS_PUBLIC_ADDR=//p' .env)

bin=${ZDXSV_BIN:-bin/zdxsv}
if [ -z "${ZDXSV_BIN:-}" ]; then
	mkdir -p bin && go build -o bin/zdxsv ./zdxsv || exit 1
fi
if command -v getcap >/dev/null && ! getcap "$bin" | grep -q net_bind_service; then
	echo "note: $bin has no cap_net_bind_service; ports 53 and 443 need it (or root)"
fi

# the server's environment: .env, but never the real database
set -a; . ./.env; set +a
export ZDXSV_DB_NAME=smoke.db
# and the TLS ports on the test keys (zdxsv/testdata), never the real one
td=zdxsv/testdata
export ZDXSV_TLS_CERT_FILE=$td/server.crt ZDXSV_TLS_KEY_FILE=$td/server.key
unset ZDXSV_LOBBY_TLS_ADDR
pin() { bash scripts/tls-keys.sh pin "$1" | head -1 | cut -d" " -f1; }
ca_pin=$(pin $td/ca.crt) backup_pin=$(pin $td/backup-ca.crt) server_pin=$(pin $td/server.crt)
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
for p in 443 8200 8203 8210; do
	check "tcp $p open" timeout 3 bash -c "</dev/tcp/127.0.0.1/$p"
done
check "dnas on 443 (DNAS answers, login pages)" "$bin" -profile=0 dnascheck 127.0.0.1:443
check "lobby on tls 8220 (CA pin)" "$bin" -profile=0 tlscheck 127.0.0.1:8220 "$ca_pin"
check "lobby on tls 8220 (server key pin)" "$bin" -profile=0 tlscheck 127.0.0.1:8220 "$server_pin"
check "lobby on tls 8220 (backup CA pin, CA pin)" "$bin" -profile=0 tlscheck 127.0.0.1:8220 "$backup_pin" "$ca_pin"
check "lobby on tls 8220 refuses a chain without the pin" sh -c "! \"$bin\" -profile=0 tlscheck 127.0.0.1:8220 $backup_pin"

stat=$(curl -sf http://127.0.0.1:8203/lbs/status)
echo "lbs/status: $stat"
check "lobby status api" sh -c "echo '$stat' | grep -q '\"platforms\":{.*\"console\".*\"emu\"'"

# zproxy (built from zproxy, or $ZPROXY_BIN) against this server: STUN gives it a public
# address, it registers, and a PS2 connection makes it ask for battle info. No user is logged
# in, so both answers are refusals.
zbin=${ZPROXY_BIN:-bin/zproxy}
if [ -z "${ZPROXY_BIN:-}" ]; then
	(cd zproxy && go build -o ../bin/zproxy .) || exit 1
fi
zport=${ZPROXY_PORT:-8250}
"$zbin" -updatecheck=false -upnp=false -lobbyapi=http://127.0.0.1:8203 -stunaddr=127.0.0.1:8201 \
	-tcpport=$zport -udpport=$zport > zproxy.log 2>&1 &
zpid=$!
trap 'kill $pid $zpid 2>/dev/null' EXIT
zlog() { # pattern: waits up to 15 s for it in zproxy.log
	for i in $(seq 1 30); do
		grep -q "$1" zproxy.log && return 0
		kill -0 $zpid 2>/dev/null || break
		sleep 0.5
	done
	grep "$1" zproxy.log
}
check "zproxy stun (public address)" zlog "UDPアドレス: \[127.0.0.1:$zport "
check "zproxy register (answered: no lobby user)" zlog "サーバー: ロビーにユーザが見つかりません"
check "zproxy accepts a PS2" bash -c "exec 3<>/dev/tcp/127.0.0.1/$zport"
check "zproxy asks for battle info" zlog "PS2との接続に成功しました"
check "zproxy battle info (answered: no battle)" zlog "Error on PollLobby() 対戦情報が見つかりません"

if [ $fails -ne 0 ]; then
	tail -40 smoke.log
	cat zproxy.log
	echo "smoke: $fails FAIL"
	exit 1
fi
echo "smoke: all PASS"
