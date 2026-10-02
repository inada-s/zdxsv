#!/usr/bin/env python3
# Writes golden.txt: DNASrep's (PHP) answer to a query for every canned packet.
# Run from the repo root against the old docker/legacyweb (git 78dc17c) on :8443:
#   docker build -t legacyweb docker/legacyweb && docker run -d -p 8443:443 legacyweb
#   python3 pkg/dnas/testdata/golden.py https://127.0.0.1:8443 > pkg/dnas/testdata/golden.txt
# Line: gateway kind packet query_len answer_len answer_sha256. Queries: see query().
import hashlib, os, subprocess, sys, tempfile

DATA = "pkg/dnas/data/www"


def query(gw, kind, packet, n):
    """Deterministic query (the same in golden_test.go): sha256 stream + header fields."""
    q = b""
    i = 0
    while len(q) < n:
        q += hashlib.sha256(f"{gw}/{kind}/{packet}/{i}".encode()).digest()
        i += 1
    q = bytearray(q[:n])
    gid, qt = packet.split("_")
    off = 0x2C if kind == "i-connect" else 0x1B
    q[0:4] = bytes.fromhex(qt)
    q[off:off + 8] = bytes.fromhex(gid)
    return bytes(q[:n])


def cases():
    for gw in sorted(os.listdir(DATA)):
        pk = sorted(os.listdir(f"{DATA}/{gw}/packets"))
        big = [p for p in pk if os.path.getsize(f"{DATA}/{gw}/packets/{p}") >= 0x148]
        for p in pk:
            yield gw, "others", p, 184
            if p in big:
                yield gw, "i-connect", p, 308
        for kind in ("others", "i-connect"):
            yield gw, kind, "0123456789abcdef_01180000", 308  # no such packet: error.raw
            yield gw, kind, big[0], 0x40  # short query: checksums over what is there


def main():
    base = sys.argv[1]
    with tempfile.TemporaryDirectory() as d:
        qf, af = f"{d}/q", f"{d}/a"
        for gw, kind, p, n in cases():
            with open(qf, "wb") as f:
                f.write(query(gw, kind, p, n))
            subprocess.run(["curl", "-sSfk", "--tlsv1.0", "--tls-max", "1.0", "--ciphers", "DEFAULT@SECLEVEL=0",
                            "--data-binary", f"@{qf}", "-o", af, f"{base}/{gw}/v2.5_{kind}"], check=True)
            a = open(af, "rb").read()
            print(gw, kind, p, n, len(a), hashlib.sha256(a).hexdigest())


main()
