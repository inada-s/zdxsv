# Deploying zdxsv with docker

This runs the whole server (DNS, DNAS front end, login, lobby, battle, status page) on one Linux host with docker compose.

## Requirements

- Linux host with a public IPv4 address, docker and the compose plugin (`docker compose`).
- Open ports:

| port | proto | service | used by |
|---|---|---|---|
| 53 | udp | dns | PS2 / emulator DNS (points the game's hosts at this server) |
| 443 | tcp | router -> dnas (DNAS, login) / https-portal (website) | everyone |
| 8200 | tcp | lobby | everyone |
| 8201 | tcp, udp | lobby RPC | zproxy (UDP proxy for real PS2) |
| 8210 | tcp, udp | battle | everyone |

Port 80 does not need to be open: the game reaches the login pages through the DNAS front end on 443.
If the host runs systemd-resolved, it already holds port 53 on 127.0.0.53; stop it or set `DNSStubListener=no`.

## Compose files

- `docker-compose.yml`: the production services. Always pass it with `-f`.
- `docker-compose.override.yml`: development only (rebuilds the Go code on start, publishes the website on 1080). docker compose loads it automatically if `-f` is not given.
- `docker-compose.prod.yml` (git-ignored): your own production additions. The `https-portal` service in `docker-compose.yml` is a placeholder (`hello-world`). Put the real website TLS front end here (`docker/https-portal` has its config), or non-console HTTPS on 443 has nowhere to go.

All commands below use `DC="docker compose -f docker-compose.yml"`. Add `-f docker-compose.prod.yml` if you have one.

## First deploy

```sh
git clone https://github.com/inada-s/zdxsv && cd zdxsv
# .env: the tracked one is a development sample; write yours with your public IP in the *_PUBLIC_ADDR lines
IP=203.0.113.10
sed "s/127.0.0.1/$IP/" <<'EOS' > .env
ZDXSV_DNAS_PUBLIC_ADDR=127.0.0.1
ZDXSV_LOGIN_ADDR=login:80
ZDXSV_LOGIN_PUBLIC_ADDR=127.0.0.1
ZDXSV_LOBBY_ADDR=lobby:8200
ZDXSV_LOBBY_RPC_ADDR=lobby:8201
ZDXSV_LOBBY_PUBLIC_ADDR=127.0.0.1:8200
ZDXSV_BATTLE_ADDR=battle:8210
ZDXSV_BATTLE_RPC_ADDR=battle:3080
ZDXSV_BATTLE_PUBLIC_ADDR=127.0.0.1:8210
ZDXSV_STATUS_ADDR=status:8080
ZDXSV_DB_NAME=zdxsv.db
EOS
docker build -f docker/zdxsv/Dockerfile . && $DC build   # zdxsv image once first, see below
# the database: create the file first, or docker mounts a directory in its place.
# initdb wipes the tables: run it on the first deploy only.
touch zdxsv.db && $DC run --rm lobby initdb
$DC up -d
```

The 5 zdxsv services (dns, login, lobby, battle, status) share one Dockerfile with a cgo sqlite build. A plain `$DC up --build` builds all 5 at once, and on the CI runner that build stalled until the job was killed (2 runs). Building the image once first lets compose take the other 4 from the cache.

## Check it

`docker/smoke.sh` builds the images, brings the stack up (it keeps an existing `.env` and `zdxsv.db`) and checks:

- every service is running;
- DNS answers the 4 game hosts (`gate1.jp.dnas.playstation.org`, `www01.kddi-mmbb.jp`, `ca1202.mmcp6`, `ca1203.mmcp6`) with your public IP;
- ports 443, 8200, 8201, 8210 accept connections;
- login answers through nginx;
- `zdxsv dnascheck router:443` (the console's SSLv2 hello, TLS 1.0 RC4) gets the expected DNAS answers and the login page through the DNAS front end (`/00000020/health`);
- the status API (`/api/stat` through nginx) has polled the lobby.

It ends with `smoke: all PASS`, else it prints the failures and the service logs. CI (`.github/workflows/compose.yml`) runs it on every push.

## Status page

The `web` service serves `website/` and proxies `/api/` to the `status` service. `/api/stat` returns the lobby and battle users (refreshed every 10 s). Each user has a `Platform`, and `Platforms` counts users per platform:

```json
"Platforms": {"console": {"LobbyUserCount": 2, "BattleUserCount": 0},
              "emu-x86/64": {"LobbyUserCount": 3, "BattleUserCount": 4}}
```

`console` is a real PS2. `emu-x86/64` is the zdxsv PCSX2 build. The lobby never puts the two in one battle, so the page shows both counts.

## Update

```sh
cp zdxsv.db zdxsv.db.bak     # with the lobby and login stopped for a consistent copy
git pull
docker build -f docker/zdxsv/Dockerfile . && $DC build   # zdxsv image once first, see below
$DC up -d
$DC run --rm lobby migratedb # only when a release says the schema changed
```

## Clients

- Real PS2: set the network setting's DNS to the server's IP.
- The zdxsv PCSX2 build maps the 4 game hosts to `153.121.44.150` (zdxsv.net) by default. To use your own server, change those 4 entries in the Network & HDD settings (Ethernet DNS hosts).

## Known limits

- `dnas` (`zdxsv dnas`, the DNAS front end) speaks only what the PS2 needs: SSLv2-format hello, TLS 1.0, RSA + RC4. Its certificates expired (cert-jp 2026-04-16); the PS2 accepts them.
