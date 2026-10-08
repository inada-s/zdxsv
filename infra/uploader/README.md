Uploader - CloudFunction
=====

Stores battle replays (`<battle_code>.pb`) posted by pcsx2 and tells the lobby
(`ZDXSV_LOBBY_OPS_ADDR`, `/ops/replay_uploaded`). A copy of gdxsv `infra/uploader`.
Settings: see `function.go`.

## Develop
```shell
# store under ./store, serve it on :8081, notify a local lobby
FUNCTION_TARGET=FunctionEntryPoint UPLOADER_LOCAL_DIR=store UPLOADER_LOCAL_ADDR=:8081 \
  UPLOADER_LOCAL_URL=http://127.0.0.1:8081 UPLOADER_LOBBY_URL=http://127.0.0.1:9880/ops/replay_uploaded \
  go run ./cmd
curl -F file=@123.pb http://127.0.0.1:8080/
```
The lobby needs `ZDXSV_LOBBY_OPS_ADDR=:9880`, `ZDXSV_LOBBY_REPLAY_URL_PREFIX=http://127.0.0.1:8081/`
and a DB with `battle_record.replay_url` (`zdxsv migratedb` on an older DB).
Players find stored replays through the lobby's public API (`ZDXSV_LOBBY_API_ADDR`, e.g. `:9881`):
`curl 'http://127.0.0.1:9881/lbs/replay?battle_code=123'` gives JSON with `replay_url` (filters: see
`pkg/lobby/api.go`, as gdxsv `/lbs/replay`).

## Deploy
```shell
gcloud functions deploy uploader \
  --region asia-northeast1 \
  --entry-point FunctionEntryPoint \
  --trigger-http \
  --runtime=go125 \
  --set-env-vars UPLOADER_BUCKET=zdxsv,UPLOADER_LOBBY_URL=http://<lobby>:9880/ops/replay_uploaded \
  --allow-unauthenticated
```
