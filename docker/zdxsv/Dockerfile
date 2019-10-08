FROM golang:alpine as builder
WORKDIR /work
RUN apk add --no-cache --virtual .zdxsv-builddeps gcc musl-dev make
COPY go.mod go.sum ./
RUN go mod download && go build ...
ADD src src
ADD pkg pkg
RUN go build -o ./bin/zdxsv \
  -tags netgo \
  -installsuffix netgo \
  --ldflags '-extldflags "-static"' \
  ./src/zdxsv


FROM alpine
WORKDIR /
COPY --from=builder /work/bin /
ENTRYPOINT ["/zdxsv"]
