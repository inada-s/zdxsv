#!/bin/sh
cd work && go build -o /zdxsv \
  -tags netgo \
  -installsuffix netgo \
  --ldflags '-extldflags "-static"' \
  ./src/zdxsv && \
  cd / && exec /zdxsv -v=3 "$@"
