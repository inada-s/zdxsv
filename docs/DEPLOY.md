# Deploying zdxsv

The whole server (DNS, DNAS with the login pages, lobby, battle) is one binary and one process,
`zdxsv`. A sample systemd unit is on the [wiki](https://github.com/inada-s/zdxsv/wiki).

## Requirements

- Linux x86_64 with a public IPv4 address
- The binary: `zdxsv` in a release's `zdxsv-server_<ver>_linux-x86_64.tar.gz`, or `make build`
- `CAP_NET_BIND_SERVICE` (or root) for ports 53 and 443
- Open ports:

| port | proto | use | public / internal | used by |
|---|---|---|---|---|
| 53 | udp | DNS | public | PS2 |
| 443 | tcp | DNAS, login pages | public | PS2 |
| 8200 | tcp | lobby | public | PS2 |
| 8201 | tcp, udp | lobby RPC, STUN | public | zproxy |
| 8203 | tcp | HTTP API, live spectating (behind the reverse proxy) | internal | — |
| 8210 | tcp, udp | battle | public | PS2, zproxy |
| 8220 | tcp | TLS lobby ([TLS.md](TLS.md)) | public | emulator |
| 8221 | udp | STUN | public | emulator |
| 8222 | udp | connectivity test | public | emulator |
| 8223 | udp | GGPO relay | public | emulator |

Internal ports listen on `127.0.0.1` only (`ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203`); open only the public ones.
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
ZDXSV_LOBBY_RELAY_ADDR=:8223
ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203
ZDXSV_DB_NAME=/var/lib/zdxsv/zdxsv.db
```

Optional:

- `ZDXSV_TLS_CERT_FILE`, `ZDXSV_TLS_KEY_FILE`: TLS ports ([TLS.md](TLS.md))
- `ZDXSV_DNAS_HTTPS_BACKEND`: where other TLS on 443 goes (e.g. a web server); refused when unset
- `ZDXSV_LOBBY_REPLAY_BUCKET`: a public GCS bucket for uploaded replays (credentials:
  `GOOGLE_APPLICATION_CREDENTIALS`)
- Emulator sign-in with Discord: `ZDXSV_DISCORD_CLIENT_ID`, `ZDXSV_DISCORD_CLIENT_SECRET`,
  `ZDXSV_AUTH_PUBLIC_URL` (e.g. `https://lbs.zdxsv.net:8443`; register
  `<ZDXSV_AUTH_PUBLIC_URL>/auth/discord/callback` as the Discord app's redirect). Serves `/auth/*`
  on `ZDXSV_LOBBY_API_ADDR`: proxy `/auth/*` there like `/lbs/*`. Keep the secret in a root-only
  `EnvironmentFile` under `/etc/zdxsv/`. In-game chat `ｄｃ` gives a carry-over code (3 min, single
  use) that links an existing account at sign-in.
- Master data from a Google Spreadsheet by a Discord slash command: see [Master data](#master-data)
- Regional relays: `zdxsv relay` on other hosts, see `RelayConfig` in `pkg/config/config.go`

## Master data

Discord `/op master-up` loads the Google Spreadsheet into the DB; `/op master` answers its link. Tables and columns: `pkg/chatops/master.go`.

Setup: set `ZDXSV_DISCORD_PUBLIC_KEY`, `ZDXSV_MASTER_SPREADSHEET_ID` and `ZDXSV_DISCORD_COMMAND_ALLOW` (user and role ids), share the sheet with the service account (viewer), point the Discord Interactions Endpoint at `/discord/interactions` on `ZDXSV_LOBBY_API_ADDR`, and register `/op` once:

```sh
curl -X POST -H "Authorization: Bot $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"op","description":"Moderator commands","default_member_permissions":"4","options":[{"type":1,"name":"master","description":"The master data spreadsheet"},{"type":1,"name":"master-up","description":"Read the spreadsheet and update the master data"}]}' \
  https://discord.com/api/v10/applications/<app id>/commands
```

## Commands

```sh
zdxsv initdb      # first deploy only: wipes the tables
zdxsv             # the server
zdxsv migratedb   # server stopped, when a release changes the schema; prints "Migration done"
```

## Logging

Flags go before the command: `zdxsv -prodlog -logfile /var/log/zdxsv.log`.

| Flag | Default | |
|---|---|---|
| `-prodlog` | off | JSON lines with the Cloud Logging keys (`severity`, `eventTime`, `message`, `caller`) and `zdxsv_version`/`zdxsv_revision`; off: console text |
| `-logfile` | stderr | file to append to; reopen after rotation with a restart or logrotate `copytruncate` |
| `-v` | 0 | 2 or more adds debug lines |

Each line is a fixed `message` plus fields (`addr`, `user_id`, `battle_code`, `error`, ...); IP addresses are logged in full.
`-logtostderr` and `-alsologtostderr` are accepted and ignored.

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
