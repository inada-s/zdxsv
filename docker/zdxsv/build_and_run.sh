#!/bin/sh
cd work
go build -o /zdxsv \
  -tags netgo \
  -installsuffix netgo \
  --ldflags '-extldflags "-static"' \
  ./src/zdxsv
cd /
/zdxsv -v=3 "$@"