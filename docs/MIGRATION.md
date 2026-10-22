# zdxsv / gdxsv ホスト名分割と Cloud Function 廃止 — 最終構成と移行手順

2026-10-18 決定。まだ着手していない。対象は VM（zdxsv.net、153.121.44.150）、
リポジトリ zdxsv / gdxsv / flycast / pcsx2、DNS（ムームードメイン）、GCP プロジェクト gdxsv-274515。

## 1. 目的

- `zdxsv.net` という名前が「ゲームサーバーの住所」と「Web サイトの URL」を兼ねている状態をやめる。
- zdxsv の Web サイトを VM から GitHub Pages に移し、VM の 443 を DNAS と login 専用にする。
- gdxsv の Cloud Function のうち lbsapi, uploader, mcsfunc の 3 本を廃止し、lbs 本体と Caddy に役割を移す。
  udptest（NAT 到達性テスト用に別 IP から UDP を 1 発送る）だけは残す。別 IP が本質的に必要で、無料の公開 STUN に代替 IP を持つものが無いため。
- zdxsv と gdxsv の構成を混ぜない。VM 共通のものは「VM の設定」として両プロジェクトの外に置く。
- 既存クライアント（flycast、PCSX2 zdxsv ビルド、zproxy、実機 PS2）と既存のブックマーク・検索結果を壊さない。

## 2. 最終構成図

```
                      ┌────────────────────────────┐        ┌────────────────────────────┐
  ブラウザ ──────────▶│ GitHub Pages                │        │ GCS                        │
                      │  zdxsv.net   (zdxsv website)│        │  bucket zdxsv: replays     │
                      │  www.zdxsv.net → 301 apex   │        │  bucket gdxsv: replays,    │
                      │  www.gdxsv.net (gdxsv web)  │        │   textures, savestates     │
                      │  gdxsv.net   → 301 www      │        └──────────▲─────────▲───────┘
                      └──────────────┬─────────────┘                   │         │
                                     │ fetch (CORS)             書き込み│         │読み出し
                                     ▼                                  │         │
 ┌──────────────────────────────────────────────────────────────────────┼─────────┼───────────┐
 │ VM 153.121.44.150 / 2001:e42:102:1106:153:121:44:150                 │         │           │
 │   lbs.zdxsv.net  lbs.gdxsv.net  (A)                                  │         │           │
 │                                                                      │         │           │
 │  ── VM 共通（systemd、どのリポジトリにも属さない）─────────          │         │           │
 │  :10022 sshd                                                          │         │           │
 │  :80, :8443 caddy  /etc/caddy/Caddyfile  →  import sites/*.caddy     │         │           │
 │     Let's Encrypt (HTTP-01 on :80)                                   │         │           │
 │     sites/zdxsv.caddy  lbs.zdxsv.net:8443 /lbs/* → 127.0.0.1:8203    │         │           │
 │     sites/gdxsv.caddy  lbs.gdxsv.net:8443 /lbs/* → 127.0.0.1:9880    │         │           │
 │                                                                      │         │           │
 │  ── zdxsv (docker compose, ~/zdxsv) ───────────────────────          │         │           │
 │  :53/udp  dns      4 ホスト名 → この IP                              │         │           │
 │  :443     dnas     PS2 の DNAS 認証 + login ページ中継 ─▶ login:80   │         │           │
 │  127.0.0.1:8203    lobby HTTP API /lbs/status, /lbs/replay（caddy からのみ）│         │           │
 │  :8200,:8201(tcp/udp),:8202/udp  lobby (host network)                │         │           │
 │  :8210(tcp/udp)                  battle (host network)               │         │           │
 │                                                                      │         │           │
 │  ── gdxsv (native, systemd, ~/gdxsv) ──────────────────────          │         │           │
 │  :9876(tcp/udp) lbs lobby   :9877(tcp/udp) mcs                        │         │           │
 │  :9879/udp relay            127.0.0.1:9880 HTTP API                   │         │           │
 │     lbs: replay_upload → GCS ────────────────────────────────────────┘         │           │
 │     lbs: GCE API で mcs/relay VM を起動（mcsfunc の Go 移植）                   │           │
 │  chatops (Discord bot)                                                         │           │
 │                                                                                │           │
 │  firewall で閉じる: 3080, 16061, 16062, 24231, 26061                            │           │
 └────────────────────────────────────────────────────────────────────────────────┼───────────┘
            ▲              ▲                 ▲                   ▲                │
            │              │                 │                   │                │
   実機 PS2          PCSX2 zdxsv        zproxy             flycast ◀─────────────┘
   DNS=VM IP         lbs.zdxsv.net      lbs.zdxsv.net      lbs.gdxsv.net
   → dnas:443        :8200/8201/8202    :8201              :9876/9877/9879
     lobby:8200      :8210 (:8443 /lbs/*)                   :8443 (/lbs/status,
     battle:8210                                             replay, upload)

   GCE (オンデマンド): gdxsv mcs / relay VM ── lbs.gdxsv.net:9876 に登録
   Cloud Function: udptest のみ（flycast → https://asia-northeast1-gdxsv-274515.cloudfunctions.net/udptest → UDP "Hello" → プレイヤー）
```

### 所有の境界

| 場所 | 所有 | 内容 |
|---|---|---|
| `/etc/caddy/Caddyfile` | VM | グローバル設定と `import sites/*.caddy` の 1 行だけ |
| `/etc/caddy/sites/zdxsv.caddy` | zdxsv リポジトリ `infra/caddy/zdxsv.caddy` のコピー | lbs.zdxsv.net の設定のみ |
| `/etc/caddy/sites/gdxsv.caddy` | gdxsv リポジトリ `infra/lbs/gdxsv.caddy` のコピー | lbs.gdxsv.net の設定のみ |
| `~/zdxsv`（compose） | zdxsv | dns, dnas, login, lobby, battle。Web 配信と TLS は持たない。HTTP API は lobby が `/lbs/*` で出す |
| `~/gdxsv`（systemd） | gdxsv | lbs, chatops。gdxsv.service / chatops.service と同じ置き方で caddy の site ファイルを持つ |

Caddy を VM 共通にする理由は、Let's Encrypt の HTTP-01 チャレンジが 80 番でしか受けられず、80 番の持ち主は 1 プロセスに限られるため。
プロジェクトごとに Caddy を分けるには DNS-01（acme-dns か API のある DNS への `_acme-challenge` CNAME 委譲）が必要になり、
部品が増える。site ファイルを分ければ、どちらのリポジトリにも相手の設定は入らない。

### ホスト名

| 名前 | 向き先 | 用途 |
|---|---|---|
| zdxsv.net | GitHub Pages（A 185.199.108〜111.153、AAAA 2606:50c0:8000〜8003::153） | zdxsv Web サイトの正式 URL |
| www.zdxsv.net | CNAME inada-s.github.io | 移行期間中の Web サイト。最終的には apex へ 301 |
| lbs.zdxsv.net | VM | zdxsv ゲームサーバー、HTTP API（8443、/lbs/*）、ssh などの管理用 |
| gdxsv.net | GitHub Pages（任意） | www へ 301。今は A レコード無し |
| www.gdxsv.net | CNAME inada-s.github.io（既存） | gdxsv Web サイト |
| lbs.gdxsv.net | VM | gdxsv ゲームサーバー、HTTP API（8443） |

### VM の最終ポート一覧

| ポート | プロセス | 外部公開 |
|---|---|---|
| 53/udp | zdxsv dns | 開 |
| 80 | caddy（ACME、http → https） | 開 |
| 443 | zdxsv dnas（PS2 専用 TLS） | 開 |
| 8443 | caddy（lbs.zdxsv.net / lbs.gdxsv.net を SNI で振り分け） | 開 |
| 8200, 8201 tcp/udp, 8202/udp | zdxsv lobby | 開 |
| 8210 tcp/udp | zdxsv battle | 開 |
| 127.0.0.1:8203 | zdxsv lobby HTTP API（/lbs/status, /lbs/replay） | ループバックのみ |
| 3080 | zdxsv battle RPC | 閉 |
| 9876 tcp/udp, 9877 tcp/udp, 9879/udp | gdxsv lbs / mcs / relay | 開 |
| 127.0.0.1:9880 | gdxsv HTTP API | ループバックのみ |
| 10022 | sshd | 開 |
| 16061, 16062, 26061 | pprof | **閉** |
| 24231, 24224, 514 | google-fluentd, rsyslog | 閉 |

今との差分: router / https-portal / web(nginx) / legacyweb / status が消え、ホストに caddy、compose に dnas が入る。
8443 と 8202/udp を開け、9880 をループバックに閉じ、16061 / 16062 を閉じる。

## 3. 設計上の決定と理由

- **apex の `zdxsv.net` は廃止しない。** flycast（`GdxLobbyServer`）、PCSX2（`kServerHost`）、zproxy、gdxsv の Cloud Function と mcsfunc が
  `zdxsv.net` を焼き込んでいる。クライアントを `lbs.*` に移し終えるまで apex は VM に向けたままにし、
  その後 GitHub Pages に向ける。これで `https://zdxsv.net/` のブックマークと検索評価は最終的に元に戻る。
- **Cloud Function は udptest 以外やめる。** lbsapi は Caddy の reverse_proxy、uploader は lbs のエンドポイント、
  mcsfunc は lbs 内の Go 実装に置き換える。GCS は残る（リプレイ配信 URL が storage.googleapis.com のため）。
  udptest は「プレイヤーが通信したことのない IP から UDP を送る」ことが目的で、VM から送ると対戦直後の再起動時に NAT の
  残存マッピングで偽陽性が出る。代替 IP を返す無料の公開 STUN サーバーは 2026-10-18 時点で見つからなかった（Google / Cloudflare は非対応、
  nextcloud は同一 IP、freeswitch はプライベート IP、stunprotocol.org は消滅）。
- **API は 8443。** 443 は DNAS 専用。8443 は「HTTPS 代替」として企業ネットワークを通りやすく、Cloudflare を前に置く場合も
  プロキシ可能なポート。両ドメインを 1 つの Caddy が SNI で振り分けるので衝突しない。
- **HTTP API のパスは両プロジェクトとも `/lbs/*`。** zdxsv の master には既に lobby の公開 API（`ZDXSV_LOBBY_API_ADDR`、`/lbs/replay`、gdxsv の命名）がある。
  別コンテナの `status` が出していた `/api/stat` はこの API に `/lbs/status` として移し、`status` サービスは廃止する。
  Caddy の設定は両プロジェクトで同じ形（`/lbs/* → 127.0.0.1:<port>`）になる。
- **Caddy は VM 共通、設定はプロジェクト別。** 理由は「所有の境界」の節。nginx との比較は 2026-10-18 に再検討し、Caddy で確定。
  性能差はこの用途（API 中継、ライブ観戦の低ビットレート配信）では出ず、ボトルネックは VM と回線。リプレイファイル本体の配信は GCS のまま。https-portal（7 年前のイメージ）と nginx の 2 コンテナが
  ホストの caddy 1 プロセスに置き換わる。

### /etc/caddy/Caddyfile（VM 共通。最終形）

```
import sites/*.caddy
```

移行期間中だけ、router からの PROXY protocol を受けるために次のグローバル設定を足す（フェーズ 5 で削除）。

```
{
    servers 172.17.0.1:8444 {
        listener_wrappers {
            proxy_protocol
            tls
        }
    }
}
import sites/*.caddy
```

`servers` のアドレスはサイトの `bind` を含めた完全なリスナーアドレスでないと一致しない（`:8444` では付かない。2026-10-22 に遭遇）。

### /etc/caddy/sites/zdxsv.caddy（zdxsv リポジトリ infra/caddy/zdxsv.caddy）

```
# 移行期間のみ。zdxsv の router が 443 のブラウザ接続をここへ流す。フェーズ 5 で削除
zdxsv.net:8444 {
    bind 172.17.0.1
    redir https://www.zdxsv.net{uri} permanent
}

http://zdxsv.net {
    redir https://www.zdxsv.net{uri} permanent
}

lbs.zdxsv.net:8443 {
    header Access-Control-Allow-Origin *
    reverse_proxy /lbs/* 127.0.0.1:8203
}
```

### /etc/caddy/sites/gdxsv.caddy（gdxsv リポジトリ infra/lbs/gdxsv.caddy）

```
lbs.gdxsv.net:8443 {
    header Access-Control-Allow-Origin *
    reverse_proxy /lbs/* 127.0.0.1:9880 {
        flush_interval -1
    }
}
```

`172.17.0.1` は docker0 の IP。compose ネットワーク内の router からホストの caddy に届く唯一の経路で、`bind` で外部からは見えないようにする。

## 4. 移行手順

各フェーズは前のフェーズが安定してから進める。「要 sudo」「要 DNS」は VM の root パスワードとムームードメインの操作が必要な作業。

### フェーズ 0: 準備（ユーザー影響なし）

DNS（要 DNS）
1. `lbs.zdxsv.net`、`lbs.gdxsv.net` に A = 153.121.44.150。AAAA は入れない（apex に AAAA が無く、現行クライアントは全員 IPv4。IPv6 対応は別件）。完了 2026-10-20。
2. `www.zdxsv.net` に CNAME = inada-s.github.io。完了 2026-10-20。
3. `~/.ssh/config` の `Host zdxsv` を `HostName lbs.zdxsv.net` に変更（VM は 1 台なので gdxsv 用の ssh エントリは作らない）。完了 2026-10-20。

VM 共通（要 sudo）
4. caddy を apt（公式リポジトリ）で入れ、`/etc/caddy/Caddyfile` を `import sites/*.caddy` にし、`/etc/caddy/sites/` を作る。
   完了 2026-10-20（caddy v2.11.7）。sites が空なので何も bind せず、80 番を持つ https-portal と衝突しないため起動済み。
   元の Caddyfile は `Caddyfile.dist`。ついでに期限切れだった Google の apt 署名鍵を `/etc/apt/trusted.gpg.d/cloud.google.gpg` に更新。
5. ファイアウォール（ufw）: 8443/tcp を開け、16061/tcp と 16062/tcp を deny で先頭に挿入（既存の `16060:16070/tcp` 許可より先に評価）。
   router → ホスト caddy 用に `allow from 172.16.0.0/12 to 172.17.0.1 port 8444 proto tcp` を追加（compose ネットワークからホスト宛は ufw の INPUT を通るため必須）。
   8202/udp は既存の `8200:8299/udp` 許可に含まれていた。9880 はフェーズ 5 まで開けたまま。完了 2026-10-20。

コード: zdxsv リポジトリ（1 本の PR: ブランチ `ai/hostname-split`、2026-10-20 実装済み）
6. `infra/caddy/zdxsv.caddy`（上記）と、`docs/DEPLOY.md` に「`/etc/caddy/sites/` へコピーして `systemctl reload caddy`」の手順。
7. lobby の公開 API に `/lbs/status` を追加（`src/zdxsv/status.go` の `makeStatus` を `pkg/lobby/api.go` に移し、lobby 自身の状態から作る。
   JSON の形は今の `/api/stat` のまま）。`status` サブコマンドと `status` サービスを削除。`ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203`。
   `stripHost()` は `*_ADDR` のホスト部を捨てて全インターフェースに bind するので、API のアドレスはホスト部を尊重するように直す（ループバックに閉じるため）。
8. `docker-compose.yml`: `https-portal`、`web`、`status` を削除。`docker/https-portal`、`docker/web` を削除。override からも `web` を外す。
   `docker/smoke.sh` は nginx 経由の確認を lobby の `/lbs/status` 直接と `dnascheck` に置き換える。
   README の「Run as local mode」は、Web サイトは配らず状態確認は `curl http://<host>:8203/lbs/status` と書き換える
   （website/ は zdxsv.net 固有の内容で、セルフホストでは配らない。決定 2026-10-18）。
9. `src/router/main.go` の転送先を `https-portal:443` から `172.17.0.1:8444` へ。
10. `.github/workflows/pages.yml`（`website/` を Pages に公開）。Pages の Source = GitHub Actions、Custom domain = www.zdxsv.net はリポジトリ設定で行う（Actions 公開では CNAME ファイルは無視される）。
11. `website/index.html`: `/api/stat` を `https://lbs.zdxsv.net:8443/lbs/status` に、`<link rel="canonical" href="https://www.zdxsv.net/">` を追加（フェーズ 5 で apex に変更）。
12. `src/zproxy/config.go` の `-lobbyrpcaddr` デフォルトを `lbs.zdxsv.net:8201` に。

コード: gdxsv リポジトリ（PR inada-s/gdxsv#432、2026-10-20 マージ済み。リリースタグはフェーズ 2 で）
13. `infra/lbs/gdxsv.caddy`（上記）。`infra/lbs/launch-lbs.sh` の `GDXSV_LOBBY_HTTP_ADDR` は `:9880` のまま（フェーズ 5 で `127.0.0.1:9880`）。
14. lbs に `/lbs/replay_upload`（multipart 受信 → GCS bucket gdxsv → 既存の replay_uploaded 処理を内部呼び出し）。
15. mcsfunc を Go に移植（`gdxsv/lbs_gce.go`、`google.golang.org/api/compute/v1`、起動スクリプトは Go 定数、lobby アドレスは `GDXSV_LOBBY_PUBLIC_ADDR=lbs.gdxsv.net:9876` から）。
    `GDXSV_MCSFUNC_URL` を廃止し `GDXSV_GCE_ONDEMAND=true` と `GDXSV_REPLAY_BUCKET=gdxsv` を追加。
    **フェーズ 2 の前に GCP IAM**: lbs のサービスアカウントに `roles/compute.instanceAdmin.v1` と `roles/iam.serviceAccountUser`、バケット gdxsv への objectCreator / objectViewer。
16. `website/src/Status.tsx` の URL を `https://lbs.gdxsv.net:8443/lbs/status` に。
17. `run4flycast.sh` のデフォルトを `lbs.gdxsv.net` に。

コード: flycast（PR inada-s/flycast#528、2026-10-20 マージ済み。gdxsv 側がフェーズ 2 で稼働してからリリース）
18. `core/gdxsv/gdxsv_config.cpp` の `GdxLobbyServer` デフォルトを `lbs.gdxsv.net` に。emu.cfg に保存済みの `server = zdxsv.net` は起動時に `lbs.gdxsv.net` へ書き換える（デフォルト変更だけでは既存ユーザーが移らない）。
19. `gdxsv_replay_util.cpp` の `ReplayApiUrl` / `LiveApiUrl` デフォルトを `https://lbs.gdxsv.net:8443/lbs` に。
20. `gdxsv_backend_rollback.cpp` のアップロード先を `https://lbs.gdxsv.net:8443/lbs/replay_upload` に。

コード: pcsx2（zdxsv ビルド。PR inada-s/pcsx2#223、2026-10-20 マージ済み。リリースはいつでも可）
21. `pcsx2/Zdxsv/ServerHosts.cpp` の `kServerHost` を `lbs.zdxsv.net` に。

### フェーズ 1: VM で master を稼働、Web サイトを www へ（完了 2026-10-22 18:35 JST、停止時間約 1 分、オンライン 0 人）

事前検証（2026-10-22 完了）: master の `zdxsv.exe` を Windows 上でネイティブ起動（dns / dnas / login / battle / lobby、`ZDXSV_DNS_ADDR=192.168.1.3:53`）し、
実機 PS2 が Go 製 dnas の DNAS 認証（i-connect / others）→ ログインページ → `/login` → ロビー入室まで通ることを確認。account / user が DB に作られた。

前提: フェーズ 0 の DNS が引けること。ロビーに人がいない時間帯に行う（再ビルドで数分止まる）。

1. バックアップ: `cp zdxsv.db zdxsv.db.bak-$(date +%F)`（lobby と login を止めてから）。
2. `git pull`。VM 側の `docker-compose.prod.yml` から `https-portal` を削除（`lobby` / `battle` の `network_mode: host` は維持）。
   実施 2026-10-22: VM の known_hosts にある GitHub の RSA 鍵が 2023 年の更新前のものだったので api.github.com/meta の鍵に差し替え、ssh 鍵が無いため remote を https に変更。`.env` は追跡ファイルで衝突するため `git checkout -- .env` 後に書き直し（`ZDXSV_LOBBY_API_ADDR=127.0.0.1:8203`、`ZDXSV_STATUS_ADDR` 削除）。バックアップ: `zdxsv.db.bak-2026-10-22`、`.env.vm-backup-2026-10-22`、`docker-compose.prod.yml.bak-2026-10-22`。
3. メモリ 961 MiB のため、`docker build --pull -f docker/zdxsv/Dockerfile .` を先に 1 回走らせてから `docker-compose up --build`。
   `--pull` 必須: VM にキャッシュされた `golang:alpine` が 2019 年のもので、`embed` 非対応のため master がビルドできない（2026-10-22 に遭遇）。
4. `docker-compose run --rm lobby migratedb`（`battle_record.replay_url` 列の追加。初回のみ）。
5. `systemctl restart zdxsv`。この時点で 80 番が空く。
6. （要 sudo）`infra/caddy/zdxsv.caddy` と gdxsv の `infra/lbs/gdxsv.caddy` を `/etc/caddy/sites/` にコピー、移行期間用のグローバル設定を
   `/etc/caddy/Caddyfile` に足し、`systemctl start caddy`。証明書 3 枚（zdxsv.net, lbs.zdxsv.net, lbs.gdxsv.net）が取れたことをログで確認。
7. 確認:
   - `zdxsv dnascheck 153.121.44.150:443` が OK（PS2 経路）。
   - `curl -sI https://zdxsv.net/` が 301 → `https://www.zdxsv.net/`。
   - `curl https://lbs.zdxsv.net:8443/lbs/status` と `curl https://lbs.gdxsv.net:8443/lbs/status` が JSON を返し、CORS ヘッダが付く。
   - 実機 PS2 または PCSX2 で DNAS → login → ロビー → 対戦まで通す。
8. GitHub: zdxsv リポジトリの Pages を有効化（Source: GitHub Actions、Custom domain: www.zdxsv.net）。完了 2026-10-20: PR #116 マージ後に https://www.zdxsv.net/ が公開済み。Enforce HTTPS は証明書発行後にオン。
   カスタムドメインの DNS チェックと HTTPS 証明書発行を待つ（数分〜1 時間）。
9. Search Console に `www.zdxsv.net` をプロパティ追加。（未実施）

完了時の確認結果（外部から）: `https://zdxsv.net/` と `http://zdxsv.net/` は 301 → `https://www.zdxsv.net/`、`lbs.zdxsv.net:8443` / `lbs.gdxsv.net:8443` の `/lbs/status` は 200 で CORS ヘッダー付き、`dnascheck 153.121.44.150:443` OK、16061 と 8203 は外から閉、9880 は予定通り開いたまま。
つまずき: compose から消えたサービスのコンテナ（https-portal、legacyweb、web、status）は unit の `down -v` では消えないので `docker rm -f` が必要。`servers :8444` では bind 付きリスナーにラッパーが付かない。

ロールバック: `systemctl stop caddy`、`git checkout deeeeef` と旧 `docker-compose.prod.yml` に戻して `systemctl restart zdxsv`。DNS は触っていないので影響なし。

### フェーズ 2: gdxsv 側の切り替えと互換シム

注意: VM の `launch-lbs.sh` は gdxsv.service の再起動のたびに GitHub の最新リリースを取ってくる。
PR #432 を含むリリースタグを打つのは、GCP IAM の付与と VM 側 `launch-lbs.sh` の差し替え（`GDXSV_GCE_ONDEMAND` / `GDXSV_REPLAY_BUCKET` / `GDXSV_LOBBY_PUBLIC_ADDR=lbs.gdxsv.net:9876`）と同時に行う。
先にリリースだけ出すと、次の再起動でオンデマンドの mcs / relay 起動が止まる。PR のマージ自体は安全。

1. gdxsv を新バージョンとしてリリース（lbs の upload / GCE 直接呼び出し）。`launch-lbs.sh` から `GDXSV_MCSFUNC_URL` を削除し、
   `systemctl restart gdxsv`。
2. `gcloud functions delete mcsfunc`（lbs 以外の呼び出し元は無いので即削除可）。
3. 互換シム: 既存の Cloud Function `lbsapi` と `uploader` の転送先を `http://zdxsv.net:9880` から `http://lbs.gdxsv.net:9880` に変えて再デプロイ。
   これで旧 flycast は apex を動かした後も動き続ける。
4. gdxsv の website を `npm run deploy` で gh-pages に反映（Status.tsx の URL 変更）。

### フェーズ 3: クライアントのリリース

1. flycast リリース（フェーズ 0 の 18〜20）。
2. PCSX2 zdxsv ビルドのリリース（フェーズ 0 の 21）。
3. zproxy リリース（自己更新機能で配布される）。
4. Discord 等で「新バージョンに更新してください」と告知。旧バージョンは apex 切り替え（フェーズ 5）まで動く。

### フェーズ 4: 待機と観測

- gdxsv lbs のログインログにクライアントのバージョンが残る。旧ホスト名を焼き込んだバージョンからの接続数を週単位で見る。
- zdxsv は lobby のログで PCSX2 / zproxy のバージョンを確認する。
- 目安: 旧バージョンの接続が 4 週間連続でほぼゼロになったらフェーズ 5 へ。期間の判断はユーザーが行う。

### フェーズ 5: apex 切り替えと撤去

DNS（要 DNS）
1. `zdxsv.net` の A を GitHub Pages の 4 つの IP に、AAAA も Pages の 4 つに（任意）。VM 向きの A を削除。
2. GitHub Pages の Custom domain を `www.zdxsv.net` から `zdxsv.net` に変更（`index.html` の canonical も `zdxsv.net` に）。
   GitHub が `www.zdxsv.net` → `zdxsv.net` の 301 を自動で返す。
3. 任意: `gdxsv.net` の A/AAAA も Pages に向ける。`www.gdxsv.net` がカスタムドメインのままなら apex → www に 301 される。

zdxsv（VM）
4. `docker-compose.yml` から `router` を外し、`dnas` に `ports: ["443:443"]` を付ける（master に PR）。`git pull` して `systemctl restart zdxsv`。
5. （要 sudo）`sites/zdxsv.caddy` の `zdxsv.net:8444` ブロックと `http://zdxsv.net` ブロック、`/etc/caddy/Caddyfile` のグローバル設定を削除して `systemctl reload caddy`。

gdxsv（VM）
6. `launch-lbs.sh` の `GDXSV_LOBBY_HTTP_ADDR` を `127.0.0.1:9880` にして `systemctl restart gdxsv`。

GCP
7. `gcloud functions delete lbsapi uploader`。udptest は残す。

確認
8. `https://zdxsv.net/`、`https://www.zdxsv.net/`（→ apex）、`https://lbs.zdxsv.net:8443/lbs/status`、`https://lbs.gdxsv.net:8443/lbs/status`。
9. `zdxsv dnascheck 153.121.44.150:443`、実機 PS2 の接続。外部から 9880 に届かないこと。
10. Search Console でアドレス変更（www → apex）を通知。

ロールバック: apex の A を VM に戻し、router を compose に戻す。Cloud Function は削除前なら触らなくてよい。

## 5. 互換期間中の状態（フェーズ 1〜4）

```
https://zdxsv.net/          → zdxsv router(443) → host caddy(172.17.0.1:8444) → 301 https://www.zdxsv.net/
https://www.zdxsv.net/      → GitHub Pages
PS2 → 443                   → zdxsv router → dnas
旧 flycast → zdxsv.net:9876 → VM（apex はまだ VM）
旧 flycast → CF lbsapi/uploader → lbs.gdxsv.net:9880（シム。9880 はこの間だけ外部に開いている）
新 flycast → lbs.gdxsv.net:9876 / :8443
```

## 6. 未決事項

- 旧クライアント切り捨ての判断基準（フェーズ 4）。
- google-fluentd（約 110 MB）を止めるかどうか。移行とは独立。
