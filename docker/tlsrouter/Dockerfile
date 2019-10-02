FROM golang:latest as builder
WORKDIR /work
RUN git clone https://github.com/google/tcpproxy
RUN cd /work/tcpproxy/cmd/tlsrouter && \
    GOOS=linux CGO_ENABLED=0 go build -ldflags="-s -w" . && \
    mv ./tlsrouter /tlsrouter

FROM scratch
COPY --from=builder /tlsrouter /tlsrouter
ADD ./tlsrouter.conf /tlsrouter.conf
CMD ["./tlsrouter", "-conf", "tlsrouter.conf"]