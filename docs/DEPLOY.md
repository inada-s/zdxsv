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

Moderators update bans, server settings, lobby settings and rules from a Google Spreadsheet with a Discord slash command. Changes apply at once, with no restart.

### Setup

1. Set `ZDXSV_DISCORD_PUBLIC_KEY` (the Discord app's public key), `ZDXSV_MASTER_SPREADSHEET_ID` and `ZDXSV_DISCORD_COMMAND_ALLOW` (comma separated Discord user and role ids that may run it; empty: nobody).
2. Use the same Google credentials as for the bucket, and share the spreadsheet with that service account as a viewer.
3. Set the Discord app's Interactions Endpoint URL to `https://<host>/discord/interactions` and proxy it to `ZDXSV_LOBBY_API_ADDR`.
4. Register `/op` once with the bot token. Permission 4 shows it only to members who can ban.

```sh
curl -X POST -H "Authorization: Bot $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"op","description":"Moderator commands","default_member_permissions":"4","options":[{"type":1,"name":"master","description":"The master data spreadsheet"},{"type":1,"name":"master-up","description":"Read the spreadsheet and update the master data"}]}' \
  https://discord.com/api/v10/applications/<app id>/commands
```

`/op master` answers the spreadsheet link. `/op master-up` reads the spreadsheet and updates the master data.

### Sheet format

- A table starts at a cell `@m_<name>` on any sheet. The header is the row below it, and the data runs to the first empty row.
- Columns whose header starts with `#` are ignored.
- If any row is invalid, nothing changes and the command answers the errors.
- `@m_ban` is required. Any other table replaces its whole DB table when present, and leaves it unchanged when absent.

### `@m_ban`

| Column | |
|---|---|
| `key` | User ID; must exist |
| `until` | End of the ban. UTC unless it has an offset (`2026/11/06 12:00:00`, `2026-11-06T21:00:00+09:00`) |
| `reason`, `created` | Optional |

A banned user's account is refused at its next login. This is the only ban list; `migratedb` drops the old `ban` table.

### `@m_server_setting`

Columns `key` and `value`. Keys missing from the table are removed.

| Key | Value |
|---|---|
| `min_pcsx2_version` | `zdxsv-X.Y.Z`; empty: no minimum |
| `pcsx2_allow` | Login keys or machine IDs that skip the version check |

### `@m_lobby_setting`

One row per lobby and platform. A lobby without a row has all the defaults.

| Column | Value | Empty |
|---|---|---|
| `platform` | `console` or `emu` | Required |
| `no` | Lobby number | Required |
| `start` | `2v2`, `1v1` or `solo` | `2v2` |
| `explain` | Text under "Lobby NN" in the lobby list | None |
| `rule_id` | An `m_rule` id | Menu defaults |
| `hack` | Comma separated, see below | None |

| `hack` | Effect |
|---|---|
| `none` | Nothing; cannot be combined |
| `pad_delay` | Fixed pad delay. Replaces `explain`; cannot be combined with `widescreen` |
| `widescreen` | The `ｗ０`/`ｗ１` patch. Replaces `explain` |
| `zproxy_test` | Entering a battle starts a zproxy connection test. Empty `explain`: `接続テスト対戦専用` |
| `chat_command` | The `ｗ０`/`ｗ１`/`ｄｃ` chat commands and their notice. `ｗ０`/`ｗ１` need a `widescreen` lobby on the platform |

The PS2 needs these rows to keep its lobbies:

| `platform` | `no` | `start` | `hack` |
|---|---|---|---|
| `console` | 1 | | `zproxy_test,chat_command` |
| `console` | 2 | `1v1` | |
| `console` | 3 | `solo` | `pad_delay` |
| `console` | 4 | | `widescreen` |

### `@m_rule`

One row per rule, with a required `id`. Each `rule_id` must exist in `m_rule`, in the sheet or else in the DB.

The other columns are numbers, not limited to the room menu's choices; empty is the menu default: `battle_count`, `battle_time` (seconds), `battle_gauge`, `visible_team`, `reload_type`, `damage_level`, `awake_type`, `awake_inc_ratio`, `awake_dec_ratio`.

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
