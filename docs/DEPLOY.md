# Deploying zdxsv with docker

This runs the whole server (DNS, DNAS front end, login, lobby, battle) on one Linux host with docker compose.
It is one container running one process, `zdxsv all`, which serves everything. The one-server commands
(`zdxsv dns`, `dnas`, `login`, `lobby`, `battle`) still exist for running the parts separately.

## Requirements

- Linux host with a public IPv4 address, docker and the compose plugin (`docker compose`).
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
`zdxsv all` does not listen on `ZDXSV_LOGIN_ADDR` or `ZDXSV_BATTLE_RPC_ADDR` at all (the login pages are
served inside dnas, the lobby drives the battle logic in process), so neither needs to be set.
It serves pprof on 16060: do not open that port.
If the host runs systemd-resolved, it already holds port 53 on 127.0.0.53; stop it or set `DNSStubListener=no`.

## Compose files

- `docker-compose.yml`: the production services. Always pass it with `-f`.
- `docker-compose.override.yml`: development only (rebuilds the Go code on start). docker compose loads it automatically if `-f` is not given.
- `docker-compose.prod.yml` (git-ignored): your own production additions (zdxsv.net runs `zdxsv` with `network_mode: host`).

All commands below use `DC="docker compose -f docker-compose.yml"`. Add `-f docker-compose.prod.yml` if you have one.

## First deploy

```sh
git clone https://github.com/inada-s/zdxsv && cd zdxsv
# .env: the tracked one is a development sample; write yours with your public IP in the *_PUBLIC_ADDR lines
IP=203.0.113.10
sed "s/127.0.0.1/$IP/" <<'EOS' > .env
ZDXSV_DNAS_PUBLIC_ADDR=127.0.0.1
ZDXSV_LOGIN_PUBLIC_ADDR=127.0.0.1
ZDXSV_LOBBY_ADDR=:8200
ZDXSV_LOBBY_RPC_ADDR=:8201
ZDXSV_LOBBY_PUBLIC_ADDR=127.0.0.1:8200
ZDXSV_BATTLE_ADDR=:8210
ZDXSV_BATTLE_PUBLIC_ADDR=127.0.0.1:8210
ZDXSV_LOBBY_API_ADDR=:8203
ZDXSV_DB_NAME=zdxsv.db
EOS
$DC build
# the database: create the file first, or docker mounts a directory in its place.
# initdb wipes the tables: run it on the first deploy only.
touch zdxsv.db && $DC run --rm zdxsv initdb
$DC up -d
```

The game ports (`ZDXSV_LOBBY_ADDR`, `ZDXSV_LOBBY_RPC_ADDR`, `ZDXSV_BATTLE_ADDR`) bind all interfaces whatever host part they have; only the port counts. The HTTP API (`ZDXSV_LOBBY_API_ADDR`) binds the host part given (`:8203` for everywhere, `127.0.0.1:8203` for loopback only).

## Check it

`docker/smoke.sh` builds the image, brings the stack up (it keeps an existing `.env` and `zdxsv.db`) and checks:

- the container is running;
- DNS answers the 4 game hosts (`gate1.jp.dnas.playstation.org`, `www01.kddi-mmbb.jp`, `ca1202.mmcp6`, `ca1203.mmcp6`) with your public IP;
- ports 443, 8200, 8201, 8203, 8210 accept connections;
- `zdxsv dnascheck 127.0.0.1:443` (the console's SSLv2 hello, TLS 1.0 RC4) gets the expected DNAS answers and the login page through the DNAS front end (`/00000020/health`);
- the lobby HTTP API answers `/lbs/status` with the platform counts.

It ends with `smoke: all PASS`, else it prints the failures and the service logs. CI (`.github/workflows/compose.yml`) runs it on every push.

## Status API

The lobby's HTTP API (`ZDXSV_LOBBY_API_ADDR`, port 8203) answers `/lbs/status` with the lobby and battle users (read from the lobby at most once per second). Each user has a `Platform`, and `Platforms` counts users per platform:

```json
"Platforms": {"console": {"LobbyUserCount": 2, "BattleUserCount": 0},
              "emu-x86/64": {"LobbyUserCount": 3, "BattleUserCount": 4}}
```

`console` is a real PS2. `emu-x86/64` is the zdxsv PCSX2 build. The lobby never puts the two in one battle, so the page shows both counts.

Unlike the game ports, the API binds the host part of its address: `ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203` with `network_mode: host` keeps it on loopback for a reverse proxy, `:8203` in the compose network publishes it on 8203.

## zdxsv.net: website and HTTPS API

The website (`website/`) is not served by the stack. `.github/workflows/pages.yml` publishes it to GitHub Pages on every push to master; the custom domain (`www.zdxsv.net`) is set in the repository's Pages settings, not by a CNAME file.

The page reads `https://lbs.zdxsv.net:8443/lbs/status`. That is caddy, installed on the host as a systemd service with `/etc/caddy/Caddyfile` = `import sites/*.caddy`; `zdxsv` runs with `network_mode: host` and `ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203`. To update the site config:

```sh
sudo cp infra/caddy/zdxsv.caddy /etc/caddy/sites/ && sudo systemctl reload caddy
```

dnas sends the non-console TLS on 443 (browsers opening https://zdxsv.net) to `ZDXSV_DNAS_HTTPS_BACKEND=172.17.0.1:8444` (set it in `.env`), caddy's `zdxsv.net:8444` block on docker0, with a PROXY header, which redirects to the website. caddy needs `listener_wrappers { proxy_protocol; tls }` for that listener in the global options (`servers 172.17.0.1:8444 { ... }`, the full bound address). Without `ZDXSV_DNAS_HTTPS_BACKEND` those connections get a TLS alert.

## Update

```sh
cp zdxsv.db zdxsv.db.bak     # with the stack stopped for a consistent copy
git pull
$DC build
$DC up -d
$DC run --rm zdxsv migratedb # only when a release says the schema changed
```

### Updating from the 6-service stack (before 2026-10)

The stack used to be 6 containers (router, dnas, dns, login, lobby, battle). To move over:

- `docker-compose.prod.yml`: replace the `lobby`/`battle` entries with one `zdxsv` service (`network_mode: host`).
- `.env`: `ZDXSV_LOGIN_ADDR` and `ZDXSV_BATTLE_RPC_ADDR` are no longer used; add `ZDXSV_DNAS_HTTPS_BACKEND=172.17.0.1:8444` if the router was sending browsers to caddy; `ZDXSV_LOBBY_ADDR`, `ZDXSV_LOBBY_RPC_ADDR`, `ZDXSV_BATTLE_ADDR` keep working with any host part.
- `$DC down` before `$DC up -d`: the old containers hold the ports.
- The firewall rule for pprof: the one process serves it on 16060 (lobby was 16061, battle 16062).

## Clients

- Real PS2: set the network setting's DNS to the server's IP.
- The zdxsv PCSX2 build maps the 4 game hosts to `153.121.44.150` (zdxsv.net) by default. To use your own server, change those 4 entries in the Network & HDD settings (Ethernet DNS hosts).

## Known limits

- `dnas` (`zdxsv dnas`, the DNAS front end) speaks only what the PS2 needs: SSLv2-format hello, TLS 1.0, RSA + RC4. Its certificates expired (cert-jp 2026-04-16); the PS2 accepts them.
