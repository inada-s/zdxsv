zdxsv
=====
Gundam vs Zeta Gundam private game server.

The brother project [gdxsv is here](https://github.com/inada-s/gdxsv).

## Introduction

[Mobile Suit Gundam: Gundam vs. Zeta Gundam](https://en.wikipedia.org/wiki/Mobile_Suit_Gundam:_Gundam_vs._Zeta_Gundam) was released on Playstation2 December 9, 2004.
This game has online mode, but the service ended in 2011. This project aims to keep the online mode alive for fans.


There are still many unimplemented parts, and there are many problems. We appreciate your contribution.

A server is running at [zdxsv.net](https://zdxsv.net) in Japan.

## Build and Run

Just call `make` to build the executable. `make test` runs the tests; `make cover` also writes a coverage report (`cover.txt`, `cover.html`).

Releases (the server for linux/amd64, and zproxy for every platform when its code changed) are built by GitHub Actions from a `v*` tag; see [docs/DEPLOY.md](docs/DEPLOY.md).

`zdxsv` command uses `ZDXSV_*` environment variables. Please see `.env` file the list of available environment variables.

To run a server for others, see [docs/DEPLOY.md](docs/DEPLOY.md).

### Commands
- `./bin/zdxsv initdb` initializes database file.
- `./bin/zdxsv` (no command, or `all`) serves the server in one process (what the systemd service runs): dns, the DNAS front end on 443 (the SSLv2/TLS 1.0 the PS2 speaks) with the login webpage behind it, lobby and battle. The login webpage and the lobby share the login sessions in memory, so they cannot run as separate processes.
- `./bin/zdxsv dns` serves only the dns server, which is used to direct the game console to your server.
- `./bin/zdxsv battle` serves battle.


## Run on this machine

```
scripts/run-local.sh
```
Builds and runs the whole server with `.env`, this machine's LAN address (`IP=<address>` overrides it) and the test TLS certificate.
Emulator clients must trust the test CA; on the PCSX2 side that is `ZDXSV_TLS_TEST_PIN=1`.
`ZDXSV_AUTH_DEV=1 scripts/run-local.sh` adds the emulator sign-in without Discord: `http://127.0.0.1:8203/auth/` (`ZDXSV_LOBBY_API_ADDR`) asks the browser for any user ID; never set it on a public server.

## Run a server

### Initialize database
```
ZDXSV_DB_NAME=zdxsv.db ./bin/zdxsv initdb
```

### Prepare .env file
There is the .env file in the root of this repository.
You must configure these environment variables to fit your environment.

The `auto-env-local` and `auto-env-dev` scripts might help you.

### Run
```
set -a; . ./.env; set +a
./bin/zdxsv           # every server in one process; ports 53 and 443 need root or cap_net_bind_service
```
The server does not serve a website (`website/` is the zdxsv.net site, published to GitHub Pages).
To see who is online, read the lobby's HTTP API: `curl http://127.0.0.1:8203/lbs/status` on the host (the API listens on loopback; publish it through a reverse proxy).
`make smoke` runs the whole thing on a throwaway database and checks each server.

