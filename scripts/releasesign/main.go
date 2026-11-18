// releasesign makes the signatures zproxy checks before it applies a self-update.
//
//	go run ./scripts/releasesign genkey <private key file>
//	    a new ed25519 key pair: the private key (base64 seed) to the file, the public key
//	    to zproxy/release_signing.pub. The private key goes into the repository secret
//	    ZDXSV_RELEASE_SIGNING_KEY and nowhere else.
//	go run ./scripts/releasesign sign <artifact> <signature>
//	    signs with ZDXSV_RELEASE_SIGNING_KEY (goreleaser's signs, .goreleaser.yml). Fails if
//	    the key is unset or does not match zproxy/release_signing.pub, so a release never ships
//	    archives the zproxy built from it would reject.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const pubKeyFile = "zproxy/release_signing.pub"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releasesign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 2 && args[0] == "genkey":
		return genkey(args[1])
	case len(args) == 3 && args[0] == "sign":
		return sign(args[1], args[2])
	}
	return errors.New("usage: releasesign genkey <private key file> | sign <artifact> <signature>")
}

func genkey(privFile string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(privFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.WriteFile(pubKeyFile, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644)
}

func sign(artifact, signature string) error {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("ZDXSV_RELEASE_SIGNING_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("ZDXSV_RELEASE_SIGNING_KEY is not set or not a base64 ed25519 seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	b, err := os.ReadFile(pubKeyFile)
	if err != nil {
		return err
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || !bytes.Equal(pub, priv.Public().(ed25519.PublicKey)) {
		return fmt.Errorf("ZDXSV_RELEASE_SIGNING_KEY does not match %s", pubKeyFile)
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		return err
	}
	return os.WriteFile(signature, ed25519.Sign(priv, data), 0o644)
}
