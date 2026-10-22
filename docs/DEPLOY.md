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
| 8203 | tcp | lobby HTTP API (`/lbs/status`, `/lbs/replay`, `/lbs/live`) | LAN only: status check with `curl http://<host>:8203/lbs/status`; do not open it to the internet |

Port 80 does not need to be open: the game reaches the login pages through the DNAS front end on 443.
`zdxsv` (all servers) does not listen on `ZDXSV_LOGIN_ADDR` or `ZDXSV_BATTLE_RPC_ADDR` at all (the login
pages are served inside dnas, the lobby drives the battle logic in process), so neither needs to be set.
pprof is on `127.0.0.1:16060` (`-profile=0` turns it off).
If the host runs systemd-resolved, it already holds port 53 on 127.0.0.53; set `DNSStubListener=no` in
`/etc/systemd/resolved.conf` and restart it.

## Releases

A `v*` tag makes `.github/workflows/release.yml` build and publish a GitHub release (`.goreleaser.yml`):
the zproxy archives for every platform (`zdxsv_<ver>_<os>_<arch>.zip`, what zproxy's self-update looks
for) and the server, `zdxsv-server_<ver>_linux-x86_64.tar.gz` (static linux/amd64 binary, `zdxsv.service`,
`zdxsv-release.sh`). The server binary passes `scripts/smoke.sh` on the runner before anything is published.

```sh
git tag v1.2.0 && git push origin v1.2.0
```

One tag releases both, so a zproxy release always carries a server build and the other way round.
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

## Check it

`scripts/smoke.sh` (`make smoke`) builds `bin/zdxsv`, runs it with `.env` on a throwaway database
(`smoke.db`, never `ZDXSV_DB_NAME`) and checks:

- DNS answers the 4 game hosts (`gate1.jp.dnas.playstation.org`, `www01.kddi-mmbb.jp`, `ca1202.mmcp6`, `ca1203.mmcp6`) with your public IP;
- ports 443, 8200, 8201, 8203, 8210 accept connections;
- `zdxsv dnascheck 127.0.0.1:443` (the console's SSLv2 hello, TLS 1.0 RC4) gets the expected DNAS answers and the login page through the DNAS front end (`/00000020/health`);
- the lobby HTTP API answers `/lbs/status` with the platform counts.

It ends with `smoke: all PASS`, else it prints the failures and the server log. Ports 53 and 443 need
`sudo setcap cap_net_bind_service=+ep bin/zdxsv` first (or root), and the ports must be free, so stop
the service before running it on the server. CI (`.github/workflows/ci.yml`) runs it on every push.

## Status API

The lobby's HTTP API (`ZDXSV_LOBBY_API_ADDR`, port 8203) answers `/lbs/status` with the lobby and battle users (read from the lobby at most once per second). Each user has a `Platform`, and `Platforms` counts users per platform:

```json
"Platforms": {"console": {"LobbyUserCount": 2, "BattleUserCount": 0},
              "emu-x86/64": {"LobbyUserCount": 3, "BattleUserCount": 4}}
```

`console` is a real PS2. `emu-x86/64` is the zdxsv PCSX2 build. The lobby never puts the two in one battle, so the page shows both counts.

## zdxsv.net: website and HTTPS API

The website (`website/`) is not served by zdxsv. `.github/workflows/pages.yml` publishes it to GitHub Pages on every push to master; the custom domain (`www.zdxsv.net`) is set in the repository's Pages settings, not by a CNAME file.

The page reads `https://lbs.zdxsv.net:8443/lbs/status`. That is caddy, installed on the host as a systemd service with `/etc/caddy/Caddyfile` = `import sites/*.caddy`; zdxsv runs with `ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203`. To update the site config:

```sh
sudo cp infra/caddy/zdxsv.caddy /etc/caddy/sites/ && sudo systemctl reload caddy
```

dnas sends the non-console TLS on 443 (browsers opening https://zdxsv.net) to `ZDXSV_DNAS_HTTPS_BACKEND=127.0.0.1:8444` (set it in `.env`), caddy's `zdxsv.net:8444` block, with a PROXY header, which redirects to the website. caddy needs `listener_wrappers { proxy_protocol; tls }` for that listener in the global options (`servers 127.0.0.1:8444 { ... }`, the full bound address). Without `ZDXSV_DNAS_HTTPS_BACKEND` those connections get a TLS alert.

## Update

```sh
cd ~/zdxsv
cp zdxsv.db zdxsv.db.bak     # with the service stopped for a consistent copy
bash releases/*/zdxsv-release.sh   # any copy of the script: downloads the latest release and restarts the service
bin/zdxsv version
bin/zdxsv migratedb          # only when a release says the schema changed (reads ZDXSV_DB_NAME from the environment: `set -a; . .env; set +a` first)
```

Old releases stay under `releases/`; to roll back, `bash releases/*/zdxsv-release.sh v1.1.0`.

## Moving from the docker stack (before 2026-10)

zdxsv used to run as docker-compose containers (6 services, then 1). The database file and `.env` stay
where they are; only the process changes.

1. In the checkout (`~/zdxsv`, which is also the service home): `curl -fsSLO https://raw.githubusercontent.com/inada-s/zdxsv/master/scripts/zdxsv-release.sh && bash zdxsv-release.sh` (no Go needed on the host; `bin/` is git-ignored).
2. `.env`: `ZDXSV_DNAS_HTTPS_BACKEND=127.0.0.1:8444` (was `172.17.0.1:8444`, the docker bridge); `ZDXSV_LOGIN_ADDR` and `ZDXSV_BATTLE_RPC_ADDR` are unused; `ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203` as before.
3. caddy: in `/etc/caddy/Caddyfile` change `servers 172.17.0.1:8444` to `servers 127.0.0.1:8444`, copy `infra/caddy/zdxsv.caddy` (binds 127.0.0.1 now), `sudo systemctl reload caddy`.
4. Stop the old service and the containers: `sudo systemctl stop zdxsv` (the unit that ran docker-compose), `docker compose -f docker-compose.yml -f docker-compose.prod.yml down` from the old checkout if anything is still up (`docker ps`).
5. Install the unit: `sudo cp infra/systemd/zdxsv.service /etc/systemd/system/zdxsv.service` (overwrites the docker-compose one), `sudo systemctl daemon-reload && sudo systemctl enable --now zdxsv`, then `journalctl -u zdxsv -f` and `curl http://127.0.0.1:8203/lbs/status`.
6. Firewall: the `172.16.0.0/12 -> 172.17.0.1:8444` rule is no longer needed; nothing else changes (pprof is on loopback now).
7. Afterwards: `docker system prune -a` reclaims the old images, or remove docker altogether.

## Clients

- Real PS2: set the network setting's DNS to the server's IP.
- The zdxsv PCSX2 build maps the 4 game hosts to `153.121.44.150` (zdxsv.net) by default. To use your own server, change those 4 entries in the Network & HDD settings (Ethernet DNS hosts).

## Known limits

- `dnas` (the DNAS front end) speaks only what the PS2 needs: SSLv2-format hello, TLS 1.0, RSA + RC4. Its certificates expired (cert-jp 2026-04-16); the PS2 accepts them.
