FROM golang:latest as builder
WORKDIR /work
COPY go.mod go.sum ./
RUN go mod download
ADD src src
ADD pkg pkg
ADD Makefile ./
RUN make

FROM golang:latest
WORKDIR /
COPY --from=builder /work/bin /
ENTRYPOINT ["/zdxsv"]