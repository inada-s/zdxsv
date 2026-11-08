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
- Master data (bans `m_ban`, server settings `m_server_setting`, lobby settings `m_lobby_setting`, rules `m_rule`) from a Google Spreadsheet by a Discord slash command
  for moderators: `ZDXSV_DISCORD_PUBLIC_KEY` (the Discord app's public key),
  `ZDXSV_DISCORD_COMMAND_ALLOW` (comma separated Discord user and moderator role ids that may run
  it; empty: nobody), `ZDXSV_MASTER_SPREADSHEET_ID`; Google credentials as for the bucket, the
  spreadsheet shared with that service account (viewer). Set the app's Interactions Endpoint URL
  to `https://<host>/discord/interactions` (proxy it to `ZDXSV_LOBBY_API_ADDR`) and register the
  command `/op` once with the bot token (`default_member_permissions` 4: shown only to members who
  can ban):
  `curl -X POST -H "Authorization: Bot $TOKEN" -H "Content-Type: application/json" -d '{"name":"op","description":"Moderator commands","default_member_permissions":"4","options":[{"type":1,"name":"master","description":"The master data spreadsheet"},{"type":1,"name":"master-up","description":"Read the spreadsheet and update the master data"}]}' https://discord.com/api/v10/applications/<app id>/commands`.
  `/op master` answers the spreadsheet link. `/op master-up` reads the table under a cell `@m_ban` (any sheet, the gdxsv layout; other
  `@m_` tables are ignored): header row `key` (a user id), `until`, optional `reason`,
  `created`; data rows down to the first empty row. In every table, a column whose header starts with `#` is
  ignored. Times like `2026/11/06 12:00:00` are UTC
  unless they carry an offset (`2026-11-06T21:00:00+09:00`). It replaces `m_ban` only when every
  row is valid (known user, no duplicate, a time), else answers the errors. A banned user's
  account is refused at its next login until `until`, with no restart. `m_ban` is the only ban
  list (the old `ban` table is dropped by `migratedb`).
  A table under `@m_server_setting` (header `key`, `value`), when present, replaces all of
  `m_server_setting` (absent: kept): `min_pcsx2_version` (`zdxsv-X.Y.Z`; empty: no minimum),
  `pcsx2_allow`. Read at each login, so it applies at once. Keys missing from the table are removed.
  A table under `@m_lobby_setting`, when present, replaces all of `m_lobby_setting` (absent: kept;
  only valid rows: else nothing changes). One row per lobby: `platform` (`console` or `emu`), `no`
  (0-25); optional `start` (`2v2`, `1v1`, `solo`: players per side a lobby battle needs), `explain`
  (text under "Lobby NN" in the lobby list), `rule_id` (an `m_rule` id) and `hack` (code the lobby
  list runs in the game: `none`, `pad_delay` (fixed pad delay), `widescreen` (the `ｗ０`/`ｗ１`
  patch)). An empty cell or a lobby without a row keeps the default (start: 1v1 in lobby 2, solo
  in 3, else 2v2; rule: the default rule, the menu defaults with the `ZDXSV_LOBBY_BATTLE_*` values;
  hack: `pad_delay` in lobby 3, `widescreen` in 4, else `none`). A hack replaces the explain;
  lobby 1 keeps its own. A platform with no `widescreen` lobby gets no `ｗ０`/`ｗ１` notice or
  command in lobby 1. A `rule_id` missing from `m_rule` at battle start: the default rule.
  A table under `@m_rule`, when present, replaces all of `m_rule` (same rules as above). One row
  per rule: `id`; optional values from the in-game menu (empty: the menu default; the
  `ZDXSV_LOBBY_BATTLE_*` values do not apply): `battle_count`, `battle_time` (seconds),
  `battle_gauge`, `visible_team`, `reload_type`, `damage_level`, `awake_type`, `awake_inc_ratio`,
  `awake_dec_ratio`. Every `rule_id` must be in `m_rule` (the sheet's, else the DB's), else
  nothing changes. Read at each battle start and lobby list view, so it applies at once.
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
