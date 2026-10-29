# TLS for the emulators

Emulators (pcsx2) reach dnas, lobby and battle through TLS ports beside the plain ones. Real PS2
consoles cannot do TLS and keep the plain ports.

| plain port | TLS port | env (TLS address) |
|---|---|---|
| 443 (dnas, login pages) | 8220 | `ZDXSV_DNAS_TLS_ADDR` |
| 8200 (lobby) | 8221 | `ZDXSV_LOBBY_TLS_ADDR` |
| 8210 (battle, TCP) | 8222 | `ZDXSV_BATTLE_TLS_ADDR` |

The client maps the plain port to the TLS port itself, so a TLS port the client does not know is
unreachable for it: change the defaults together with pcsx2's table.

The ports open when `ZDXSV_TLS_CERT_FILE` (PEM: the server certificate, then the CA certificate)
and `ZDXSV_TLS_KEY_FILE` (the server key) are set. Open the three ports in the firewall.

## Trust

The client trusts no public CA and checks no hostname or expiry. It accepts a server when any
certificate in the chain the server sends has a public key equal to one of its pins (SHA-256 of the
SubjectPublicKeyInfo, hex). pcsx2 pins two keys: the zdxsv CA and the backup CA.

Keys (all ECDSA P-256, made by `scripts/tls-keys.sh`, needs openssl):

| key | where | secret |
|---|---|---|
| CA, backup CA (`ca.key`, `backup-ca.key`) | offline only: encrypted files, kept apart from each other, never on a server or in a repository | yes |
| server key (`server.key`) | the production server only: a file readable by the zdxsv user only, outside the repository and any image | yes |
| certificates, pins | anywhere | no |

## Making the keys

On an offline machine:

```sh
scripts/tls-keys.sh ca keys                    # asks a pass phrase for each CA key; prints both pins
scripts/tls-keys.sh server keys ca server      # server.key, server.crt (server + CA); prints the pins
```

Paste the two CA pins into pcsx2 (`pcsx2/Zdxsv/`). Copy `server.key` and `server.crt` to the
server (`~/zdxsv/tls/`, `chmod 600 server.key`, owner `zdxsv`) and add to `.env`:

```sh
ZDXSV_TLS_CERT_FILE=/home/zdxsv/zdxsv/tls/server.crt
ZDXSV_TLS_KEY_FILE=/home/zdxsv/zdxsv/tls/server.key
```

Keep `ca.key` and `backup-ca.key` on separate offline media; never copy them to the server.
`scripts/tls-keys.sh pin <pem>` prints the pin of each certificate in a file.

Check from anywhere: `zdxsv tlscheck <host>:8221 <CA pin>` (lobby, prints the chain) and
`zdxsv dnascheck <host>:8220 <CA pin>` (DNAS through TLS).

## Rotation and recovery

- New server key (leaked, lost, or routine): sign a new one with the CA (`tls-keys.sh server keys
  ca server2`), swap the two files on the server, restart. No client release.
- CA key leaked or lost: sign a server key with the backup CA (`tls-keys.sh server keys backup-ca
  server2`) and deploy it; clients already pin the backup CA. Then make a new backup CA and
  release a client whose pins are the backup CA and the new backup. Until players update, the old
  CA still works for them, so a leaked CA key stays usable against old clients until then.
- The auto updater does not use these pins (it uses GitHub's HTTPS with normal certificate checks).

## Tests

`pkg/tlsconf/testdata` holds a test CA, backup CA and two server keys (`server` signed by the CA,
`server-backup` by the backup CA), made with `TLS_KEYS_NOPASS=1 scripts/tls-keys.sh`. They are
public: nothing in production may trust them. `scripts/smoke.sh` runs the TLS ports on them.
