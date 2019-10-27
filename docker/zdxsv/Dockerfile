FROM golang:alpine as builder
WORKDIR /work
RUN apk add --no-cache --virtual .zdxsv-builddeps gcc musl-dev make
COPY go.mod go.sum ./
RUN go mod download
ADD src src
ADD pkg pkg
RUN go build -o /zdxsv \
  -tags netgo \
  -installsuffix netgo \
  --ldflags '-extldflags "-static"' \
  ./src/zdxsv

WORKDIR /
COPY docker/zdxsv/build_and_run.sh .


FROM alpine
WORKDIR /
COPY --from=builder /zdxsv /zdxsv
ENTRYPOINT ["/zdxsv"]
