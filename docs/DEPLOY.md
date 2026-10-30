# Deploying zdxsv

The whole server (DNS, DNAS front end with the login pages, lobby, battle) is one binary and
one process: `zdxsv` with no command (same as `zdxsv all`). It runs on one Linux host as a
systemd service. The one-server commands (`zdxsv dns`, `dnas`, `login`, `lobby`, `battle`)
still exist for running the parts separately.

## Requirements

- Linux x86_64 host with a public IPv4 address. The server binary comes from the GitHub
  releases (static, no runtime dependencies); Go is only needed to build from source.
- Open ports:

| port | proto | service | used by |
|---|---|---|---|
| 53 | udp | dns | PS2 / emulator DNS (points the game's hosts at this server) |
| 443 | tcp | dnas (DNAS, login pages); other TLS goes to `ZDXSV_DNAS_HTTPS_BACKEND` (zdxsv.net: the host's caddy, see below), or is refused when unset | everyone |
| 8200 | tcp | lobby | everyone |
| 8201 | tcp, udp | lobby RPC, STUN | zproxy (UDP proxy for real PS2), emulators |
| 8202 | udp | STUN test socket (lobby RPC port + 1) | emulators (connectivity test: NAT type, open port) |
| 8210 | tcp, udp | battle | everyone |
| 8204 | udp | GGPO relay (`ZDXSV_LOBBY_RELAY_ADDR`), offered in the battle info when every player of the battle reports `relay_server=1`: the path of a pair whose NATs cannot reach each other directly | emulators |
| 8220, 8221, 8222 | tcp | TLS ports of dnas, lobby, battle ([TLS.md](TLS.md)); open only with `ZDXSV_TLS_CERT_FILE` set | emulators |
| 8203 | tcp | lobby HTTP API (`/lbs/status`, `/lbs/replay`, `/lbs/live`, `/lbs/replay_upload`) | LAN only: status check with `curl http://<host>:8203/lbs/status`; do not open it to the internet |

Port 80 does not need to be open: the game reaches the login pages through the DNAS front end on 443.
`zdxsv` (all servers) does not listen on `ZDXSV_LOGIN_ADDR` or `ZDXSV_BATTLE_RPC_ADDR` at all (the login
pages are served inside dnas, the lobby drives the battle logic in process), so neither needs to be set.
pprof is on `127.0.0.1:16060` (`-profile=0` turns it off).
If the host runs systemd-resolved, it already holds port 53 on 127.0.0.53; set `DNSStubListener=no` in
`/etc/systemd/resolved.conf` and restart it.
With ufw, allow every port in the table, the UDP ones too (`sudo ufw allow 53/udp`, `8201/udp`, `8202/udp`, `8204/udp`,
`8210/udp`); only `8203` stays closed.

## Releases

A `v*` tag makes `.github/workflows/release.yml` build and publish a GitHub release (`.goreleaser.yml`):
the zproxy archives for every platform (`zdxsv_<ver>_<os>_<arch>.zip`, what zproxy's self-update looks
for) and the server, `zdxsv-server_<ver>_linux-x86_64.tar.gz` (static linux/amd64 binary, `zdxsv.service`,
`zdxsv-release.sh`). The server binary passes `scripts/smoke.sh` on the runner before anything is published.

```sh
git tag v1.2.0 && git push origin v1.2.0
```

Every tag releases the server. zproxy is only built when its code changed since the last release that
shipped it (`scripts/zproxy-release-needed.sh` compares the packages zproxy depends on, `go.mod`, `go.sum`
and `.goreleaser.yml` against that release's tag): a server-only fix as `v1.2.1` does not make every
player's zproxy self-update to an identical build. zproxy's self-update only looks at releases that
carry a zproxy archive, so releases without one are invisible to it, and `zdxsv-release.sh` always finds
the server in the latest release.
The server tarball's name must never end in `<os>_<arch>` or `<os>-<arch>`: zproxy's self-update
(old versions included) takes any such asset as its own. `src/zproxy/release_test.go` enforces it.

## First deploy

As the service user (`zdxsv` below, home `/home/zdxsv`; `infra/systemd/zdxsv.service` has these paths).
`zdxsv-release.sh` downloads a release into `~/zdxsv/releases/<tag>/`, checks it against `checksums.txt`,
points `~/zdxsv/bin/zdxsv` at it and restarts the service if it is installed.

```sh
mkdir -p ~/zdxsv && cd ~/zdxsv
curl -fsSLO https://raw.githubusercontent.com/inada-s/zdxsv/master/scripts/zdxsv-release.sh
bash zdxsv-release.sh      # latest release; `bash zdxsv-release.sh v1.2.0` for one in particular
# .env: write yours with your public IP in the *_PUBLIC_ADDR lines
IP=203.0.113.10
sed "s/127.0.0.1/$IP/" <<'EOS' > .env
ZDXSV_DNAS_PUBLIC_ADDR=127.0.0.1
ZDXSV_LOGIN_PUBLIC_ADDR=127.0.0.1
ZDXSV_LOBBY_ADDR=:8200
ZDXSV_LOBBY_RPC_ADDR=:8201
ZDXSV_LOBBY_PUBLIC_ADDR=127.0.0.1:8200
ZDXSV_BATTLE_ADDR=:8210
ZDXSV_BATTLE_PUBLIC_ADDR=127.0.0.1:8210
ZDXSV_LOBBY_RELAY_ADDR=:8204
ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203
ZDXSV_DB_NAME=zdxsv.db
EOS
# initdb wipes the tables: run it on the first deploy only.
ZDXSV_DB_NAME=zdxsv.db bin/zdxsv initdb
sudo cp releases/*/zdxsv.service /etc/systemd/system/   # or infra/systemd/zdxsv.service from a checkout
sudo systemctl daemon-reload && sudo systemctl enable --now zdxsv
journalctl -u zdxsv -f
```

To run from source instead: `git clone` the repo as `~/zdxsv`, `make build` (needs Go and gcc for
the sqlite driver), and the unit runs `bin/zdxsv` just the same.

The unit gives the process `CAP_NET_BIND_SERVICE` for ports 53 and 443, so it runs as the `zdxsv`
user. `.env` is the unit's `EnvironmentFile` (one `NAME=value` per line, no quotes needed).

The game ports (`ZDXSV_LOBBY_ADDR`, `ZDXSV_LOBBY_RPC_ADDR`, `ZDXSV_BATTLE_ADDR`) bind all interfaces
whatever host part they have; only the port counts. The HTTP API (`ZDXSV_LOBBY_API_ADDR`) binds the
host part given (`:8203` for everywhere, `127.0.0.1:8203` for loopback only).

The GGPO relay (`ZDXSV_LOBBY_RELAY_ADDR=:8204`, UDP) binds the address given and is offered to players at
`ZDXSV_LOBBY_PUBLIC_ADDR`'s host with the relay port (`ZDXSV_LOBBY_RELAY_PUBLIC_ADDR` overrides it,
`ZDXSV_LOBBY_RELAY_PUBLIC_ADDR6` adds an IPv6 one). Without it a pair whose NATs cannot reach each other has no
path and its battle is cut.

Replay upload (`POST /lbs/replay_upload`, pcsx2 posts each lobby battle's `.pb`, as gdxsv lbs):
`ZDXSV_LOBBY_REPLAY_BUCKET=<public GCS bucket>` stores them gzip encoded as `replays/<battle_code>.pb`
(credentials: the VM's service account or `GOOGLE_APPLICATION_CREDENTIALS`); off when unset. Players
reach it through caddy (`lbs.zdxsv.net:8443/lbs/*`).

## Check it

`scripts/smoke.sh` (`make smoke`) builds `bin/zdxsv`, runs it with `.env` on a throwaway database
(`smoke.db`, never `ZDXSV_DB_NAME`) and checks:

- DNS answers the 4 game hosts (`gate1.jp.dnas.playstation.org`, `www01.kddi-mmbb.jp`, `ca1202.mmcp6`, `ca1203.mmcp6`) with your public IP;
- ports 443, 8200, 8201, 8203, 8210 accept connections;
- `zdxsv dnascheck 127.0.0.1:443` (the console's SSLv2 hello, TLS 1.0 RC4) gets the expected DNAS answers and the login page through the DNAS front end (`/00000020/health`);
- on the test keys (`pkg/tlsconf/testdata`, never `.env`'s): the TLS ports 8220-8222 accept the test CA, backup CA and server key pins and refuse a chain without the pin; `dnascheck` passes through 8220;
- the lobby HTTP API answers `/lbs/status` with the platform counts.

It ends with `smoke: all PASS`, else it prints the failures and the server log. Ports 53 and 443 need
`sudo setcap cap_net_bind_service=+ep bin/zdxsv` first (or root), and the ports must be free, so stop
the service before running it on the server. The DNS check queries the port of `ZDXSV_DNS_ADDR` (default 53).
CI (`.github/workflows/ci.yml`) runs it on every push with `ZDXSV_DNS_ADDR=127.0.0.1:10053`: the runner may use port 53.

## Status API

The lobby's HTTP API (`ZDXSV_LOBBY_API_ADDR`, port 8203) answers `/lbs/status` with the lobby and battle users (read from the lobby at most once per second). Each user has a `Platform`, and `Platforms` counts users per platform:

```json
"Platforms": {"console": {"LobbyUserCount": 2, "BattleUserCount": 0},
              "emu": {"LobbyUserCount": 3, "BattleUserCount": 4}}
```

`console` is a real PS2. `emu` is every zdxsv PCSX2 build, whatever its CPU (ARM64 and x86_64 builds play in the same lobbies and battles; the CPU stays in the platform info log line). The lobby never puts the two in one battle, so the page shows both counts.

## Bans

The lobby records each account's last login ip and the `machine_id` its emulator sends, and refuses a login (`YOU ARE BANNED`) when a ban matches: `user` (a user id: every account holding it, also consoles, which send no machine id), `machine` (every account on that PC) or `ip`. Manage them on the private API (set `ZDXSV_LOBBY_OPS_ADDR=127.0.0.1:<port>` in `.env`; off when empty), after `migratedb` on an older DB:

```sh
curl "$ZDXSV_LOBBY_OPS_ADDR/ops/accounts?machine_id=M"        # accounts last logged in from M, with their users
curl "$ZDXSV_LOBBY_OPS_ADDR/ops/ban?kind=machine&value=M&reason=R"
curl "$ZDXSV_LOBBY_OPS_ADDR/ops/bans"
curl "$ZDXSV_LOBBY_OPS_ADDR/ops/unban?kind=machine&value=M"
```

A ban applies at the next login; a player already in the lobby stays until they leave.

## zdxsv.net: website and HTTPS API

The website (`website/`) is not served by zdxsv. `.github/workflows/pages.yml` publishes it to GitHub Pages on every push to master; the custom domain (`www.zdxsv.net`) is set in the repository's Pages settings, not by a CNAME file.

The page reads `https://lbs.zdxsv.net:8443/lbs/status`. That is caddy, installed on the host as a systemd service with `/etc/caddy/Caddyfile` = `import sites/*.caddy`; zdxsv runs with `ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203`. To update the site config:

```sh
sudo cp infra/caddy/zdxsv.caddy /etc/caddy/sites/ && sudo systemctl reload caddy
```

dnas sends the non-console TLS on 443 (browsers opening https://zdxsv.net) to `ZDXSV_DNAS_HTTPS_BACKEND=127.0.0.1:8444` (set it in `.env`), caddy's `zdxsv.net:8444` block, with a PROXY header, which redirects to the website. caddy needs `listener_wrappers { proxy_protocol; tls }` for that listener in the global options (`servers 127.0.0.1:8444 { ... }`, the full bound address). Without `ZDXSV_DNAS_HTTPS_BACKEND` those connections get a TLS alert.

zdxsv.net reboots every morning, so both services must come back on their own: `zdxsv.service` has
`Restart=always` and only needs the network; caddy's unit is given the same via
`/etc/systemd/system/caddy.service.d/override.conf` (`After=network-online.target`, `Wants=network-online.target`,
`Restart=on-failure`, `RestartSec=5s`). Neither may depend on anything else, and anything new on the host must be
an enabled unit as well. After a change, `systemctl is-enabled zdxsv caddy` and check both after the next reboot.

## Update

```sh
cd ~/zdxsv
cp zdxsv.db zdxsv.db.bak     # with the service stopped for a consistent copy
bash releases/*/zdxsv-release.sh   # any copy of the script: downloads the latest release and restarts the service
bin/zdxsv version
bin/zdxsv migratedb          # only when a release says the schema changed (reads ZDXSV_DB_NAME from the environment: `set -a; . .env; set +a` first)
```

Old releases stay under `releases/`; to roll back, run `zdxsv-release.sh` with an older tag (v1.2.0 is the first with a server tarball).

## Clients

- Real PS2: set the network setting's DNS to the server's IP.
- The zdxsv PCSX2 build maps the 4 game hosts to `153.121.44.150` (zdxsv.net) by default. To use your own server, change those 4 entries in the Network & HDD settings (Ethernet DNS hosts).

## Known limits

- `dnas` (the DNAS front end) speaks only what the PS2 needs: SSLv2-format hello, TLS 1.0, RSA + RC4. Its certificates expired (cert-jp 2026-04-16); the PS2 accepts them.
