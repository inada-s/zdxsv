# Deploying zdxsv

The whole server (DNS, DNAS with the login pages, lobby, battle) is one binary and one process,
`zdxsv`. A sample systemd unit is on the [wiki](https://github.com/inada-s/zdxsv/wiki).

## Requirements

- Linux x86_64 with a public IPv4 address
- The binary: `zdxsv` in a release's `zdxsv-server_<ver>_linux-x86_64.tar.gz`, or `make build`
- `CAP_NET_BIND_SERVICE` (or root) for ports 53 and 443
- Open ports:

| port | proto | |
|---|---|---|
| 53 | udp | DNS |
| 443 | tcp | DNAS, login pages |
| 8200 | tcp | lobby |
| 8201 | tcp, udp | lobby RPC, STUN |
| 8202 | udp | connectivity test |
| 8204 | udp | GGPO relay |
| 8210 | tcp, udp | battle |
| 8221 | tcp | TLS lobby for emulators ([TLS.md](TLS.md)) |

The HTTP API (8203) is for the host only; put a reverse proxy in front to publish it.
If systemd-resolved holds port 53, set `DNSStubListener=no` in `/etc/systemd/resolved.conf`.

## Configuration

`ZDXSV_*` environment variables, with your public IP:

```sh
ZDXSV_DNAS_PUBLIC_ADDR=203.0.113.10
ZDXSV_LOGIN_PUBLIC_ADDR=203.0.113.10
ZDXSV_LOBBY_ADDR=:8200
ZDXSV_LOBBY_RPC_ADDR=:8201
ZDXSV_LOBBY_PUBLIC_ADDR=203.0.113.10:8200
ZDXSV_BATTLE_ADDR=:8210
ZDXSV_BATTLE_PUBLIC_ADDR=203.0.113.10:8210
ZDXSV_LOBBY_RELAY_ADDR=:8204
ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203
ZDXSV_DB_NAME=/var/lib/zdxsv/zdxsv.db
```

Optional:

- `ZDXSV_TLS_CERT_FILE`, `ZDXSV_TLS_KEY_FILE`: TLS ports ([TLS.md](TLS.md))
- `ZDXSV_DNAS_HTTPS_BACKEND`: where other TLS on 443 goes (e.g. a web server); refused when unset
- `ZDXSV_LOBBY_REPLAY_BUCKET`: a public GCS bucket for uploaded replays (credentials:
  `GOOGLE_APPLICATION_CREDENTIALS`)
- `ZDXSV_LOBBY_OPS_ADDR`: the private API for bans (`/ops/ban`, `/ops/unban`, `/ops/bans`,
  `/ops/accounts`); keep it on 127.0.0.1
- Emulator sign-in with Discord: `ZDXSV_DISCORD_CLIENT_ID`, `ZDXSV_DISCORD_CLIENT_SECRET`,
  `ZDXSV_AUTH_PUBLIC_URL` (e.g. `https://lbs.zdxsv.net:8443`; register
  `<ZDXSV_AUTH_PUBLIC_URL>/auth/discord/callback` as the Discord app's redirect). Serves `/auth/*`
  on `ZDXSV_LOBBY_API_ADDR`: proxy `/auth/*` there like `/lbs/*`. Keep the secret in a root-only
  `EnvironmentFile` under `/etc/zdxsv/`. In-game chat `ｄｃ` gives a carry-over code (10 min, single
  use) that links an existing account at sign-in.
- Regional relays: `zdxsv relay` on other hosts, see `RelayConfig` in `pkg/config/config.go`

## Commands

```sh
zdxsv initdb      # first deploy only: wipes the tables
zdxsv             # the server
zdxsv migratedb   # server stopped, when a release changes the schema; prints "Migration done"
```

## Update

Replace the binary, run `zdxsv migratedb` if the release changes the schema, restart.
Back up the live database with `sqlite3 <db> ".backup <file>"`, not `cp`.

## Check

`make smoke` runs the server on a throwaway database and checks every port, DNAS and the API.
On the server, stop the service first. `zdxsv dnascheck <host>:443` checks DNAS from anywhere.

## Releases

`git tag vX.Y.Z && git push origin vX.Y.Z` publishes the server and, when its code changed,
zproxy (`scripts/zproxy-release-needed.sh`). The server tarball's name must never end in
`<os>_<arch>`: zproxy's self-update would take it (`src/zproxy/release_test.go`).

## Clients

- Real PS2: set the DNS to the server's IP.
- zdxsv PCSX2: change the 4 game hosts in Network & HDD settings (default: zdxsv.net).
