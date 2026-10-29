#!/bin/bash
# Keys of the emulators' TLS ports (docs/TLS.md). ECDSA P-256, needs openssl.
#   tls-keys.sh ca <dir>                  the CA and the backup CA (ca, backup-ca); prints their pins
#   tls-keys.sh server <dir> <ca> <out>   a server key signed by <dir>/<ca>.key: <out>.key, and
#                                         <out>.crt = the server certificate + the CA certificate
#                                         (ZDXSV_TLS_KEY_FILE, ZDXSV_TLS_CERT_FILE); prints its pin
#   tls-keys.sh pin <pem>...              the pin of each certificate in each PEM file
# CA keys are written encrypted (openssl asks for the pass phrase); TLS_KEYS_NOPASS=1 writes them
# plain, for test data only.
set -euo pipefail

pin_one() { # one PEM certificate on stdin
	openssl x509 -noout -pubkey | openssl pkey -pubin -outform der | openssl dgst -sha256 -r | cut -d' ' -f1
}

pin() {
	local f
	for f in "$@"; do
		awk -v d="$(mktemp -d)" '/BEGIN CERT/{n++} n{print > (d "/" n ".pem")} END{print d, n}' "$f" | {
			read -r d n
			for i in $(seq 1 "$n"); do
				echo "$(pin_one < "$d/$i.pem") $(openssl x509 -noout -subject < "$d/$i.pem")"
			done
			rm -r "$d"
		}
	done
}

cipher=(-aes-256-cbc)
[ "${TLS_KEYS_NOPASS:-}" = 1 ] && cipher=()

# The system openssl.cnf may add extensions of its own (a second basicConstraints makes the
# certificate invalid), so every request uses this one.
cnf() { # file
	cat > "$1" <<'EOS'
[req]
distinguished_name = dn
prompt = no
[dn]
[ca]
basicConstraints = critical,CA:true
keyUsage = critical,keyCertSign
subjectKeyIdentifier = hash
[server]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = serverAuth
authorityKeyIdentifier = keyid
EOS
}

case "${1:-}" in
ca)
	dir=$2
	mkdir -p "$dir"
	tmp=$(mktemp -d "$dir/tmp.XXXXXX")
	cnf "$tmp/cnf"
	for name in ca backup-ca; do
		[ -e "$dir/$name.key" ] && { echo "$dir/$name.key exists" >&2; exit 1; }
		openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 "${cipher[@]}" -out "$dir/$name.key"
		openssl req -new -x509 -config "$tmp/cnf" -extensions ca -key "$dir/$name.key" \
			-subj "/CN=zdxsv $name" -days 36500 -out "$dir/$name.crt"
	done
	rm -r "$tmp"
	pin "$dir/ca.crt" "$dir/backup-ca.crt"
	;;
server)
	dir=$2 ca=$3 out=$4
	[ -e "$out.key" ] && { echo "$out.key exists" >&2; exit 1; }
	(umask 077 && openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$out.key")
	tmp=$(mktemp -d "$out.XXXXXX")
	cnf "$tmp/cnf"
	openssl req -new -config "$tmp/cnf" -key "$out.key" -subj "/CN=zdxsv server" -out "$tmp/server.csr"
	openssl x509 -req -in "$tmp/server.csr" -CA "$dir/$ca.crt" -CAkey "$dir/$ca.key" \
		-CAcreateserial -CAserial "$tmp/serial" -days 36500 -extfile "$tmp/cnf" -extensions server \
		-out "$tmp/server.crt"
	cat "$tmp/server.crt" "$dir/$ca.crt" > "$out.crt"
	rm -r "$tmp"
	pin "$out.crt"
	;;
pin)
	shift
	pin "$@"
	;;
*)
	sed -n '2,9p' "$0" >&2
	exit 1
	;;
esac
