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

`zdxsv` command uses `ZDXSV_*` environment variables. Please see `.env` file the list of available environment variables.

To run a server with docker, see [docs/DEPLOY.md](docs/DEPLOY.md).

### Commands
- `./bin/zdxsv initdb` initializes database file.
- `./bin/zdxsv dns` serves dns server, which is used to direct the game console to your server.
- `./bin/zdxsv login` serves login webpage. Aside from this, front-end server that supports SSLv2 is required.
- `./bin/zdxsv lobby` serves lobby.
- `./bin/zdxsv battle` serves battle.


## Run with docker-compose

In order to host actually connectable server, additional services are required.
All services are configured in `docker-compose` file.

Before running docker-compose, you must prepare some file with following way.


### Initialize database
```
ZDXSV_DB_NAME=zdxsv.db ./bin/zdxsv initdb
```

### Build router binary
```
make router
```

That behaves as a proxy server on 443, which detects whether an incoming connection is a game console and proxies it to the dnas server (anything else goes to the host's caddy, see [docs/DEPLOY.md](docs/DEPLOY.md)).


### Prepare .env file
There is the .env file in the root of this repository.
You must configure these environment variables to fit your environment.

The `auto-env-local` and `auto-env-dev` scripts might help you.


### Run
In default, `docker-compose.yml` and `docker-compose.override.yml` are used and it runs as local mode which aims to serve on LAN.
When you want to server on WAN, you should use dev-server mode.

Run as local mode.
```
docker-compose up --build
```
The server does not serve a website (`website/` is the zdxsv.net site, published to GitHub Pages).
To see who is online, read the lobby's HTTP API: `curl http://<host>:8203/lbs/status`.

Run as dev-server mode.
```
docker-compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

