# udptest (Cloud Function)

Reachability check for the emulator connectivity test. The client asks
for `addr=<public ip>:<port>`; the function sends one UDP datagram
`Hello` from Google's network to that address. A client that receives it
can be reached from an address it never sent to.

- `GET ?addr=ip:port` or `POST` with an `addr` form field (multipart or
  urlencoded).
- `200 Sent`, `400` for a missing or bad addr (IP literal only, public
  unicast only, port 1-65535), `405` for other methods.

## Run locally

```shell
cd infra/udptest
FUNCTION_TARGET=FunctionEntryPoint UDPTEST_ALLOW_PRIVATE=1 PORT=8080 go run ./cmd
curl -d addr=127.0.0.1:29711 http://127.0.0.1:8080/
```

`UDPTEST_ALLOW_PRIVATE=1` allows loopback and private addresses; never
set it on the deployed function.

## Deploy (zdxsv GCP project)

```shell
cd infra/udptest
gcloud functions deploy udptest \
  --gen2 \
  --region asia-northeast1 \
  --entry-point FunctionEntryPoint \
  --trigger-http \
  --runtime go123 \
  --allow-unauthenticated \
  --max-instances 3
```

The URL it prints goes in the client's UDP test URL setting.
