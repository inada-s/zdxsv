package main

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

// releaseSigningPub checks the <archive>.sig that scripts/releasesign makes for every zproxy
// archive of a release; an update without a valid one is not applied.
//
//go:embed release_signing.pub
var releaseSigningPub string

type signatureValidator struct {
	pub ed25519.PublicKey
}

func newSignatureValidator(b64 string) (*signatureValidator, error) {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("no valid release signing key")
	}
	return &signatureValidator{pub: pub}, nil
}

func (v *signatureValidator) Validate(release, sig []byte) error {
	if !ed25519.Verify(v.pub, release, sig) {
		return errors.New("bad release signature")
	}
	return nil
}

func (v *signatureValidator) Suffix() string { return ".sig" }

var (
	// These variables are automatically assigned during release process.
	// `-s -w -X main.releaseVersion ={{.Version}} -X main.releaseCommit={{.ShortCommit}} -X main.releaseDate={{.Date}}
	releaseVersion = "local"
	releaseCommit  = "local"
	releaseDate    = "local"
)

func printReleaseInfo() {
	log.Println("releaseVersion", releaseVersion)
	log.Println("releaseCommit", releaseCommit)
	log.Println("releaseDate", releaseDate)
}

// updatedEnv is set for the process restarted after an update: it skips the check, so a
// release whose archive does not carry its own version cannot restart forever.
const updatedEnv = "ZPROXY_UPDATED"

func doSelfUpdate() {
	if releaseVersion == "local" || os.Getenv(updatedEnv) != "" {
		return
	}
	log.Println("アップデートチェックを行います")

	// Only the zproxy archives (zdxsv_<ver>_<os>_<arch>.zip): the same releases also carry
	// the server tarball (.goreleaser.yml).
	validator, err := newSignatureValidator(releaseSigningPub)
	if err != nil {
		log.Println("アップデートチェックに失敗しました", err)
		return
	}
	up, err := selfupdate.NewUpdater(selfupdate.Config{Filters: []string{`^zdxsv_`}, Validator: validator})
	if err != nil {
		log.Println("アップデートチェックに失敗しました", err)
		return
	}
	latest, found, err := up.DetectLatest("inada-s/zdxsv")
	if err != nil {
		log.Println("アップデートチェックに失敗しました", err)
		return
	}
	if !found {
		log.Println("既に最新版です")
		return
	}

	log.Println("最新版>", latest.Version)

	v := semver.MustParse(releaseVersion)
	if latest.Version.LTE(v) {
		log.Println("既に最新版です")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		log.Println("アップデートチェックに失敗しました", err)
		return
	}

	log.Println("アップデート中...")
	if err := up.UpdateTo(latest, exe); err != nil {
		log.Println("アップデートに失敗しました", err)
		return
	}

	log.Println("アップデートに成功しました")
	log.Println("新しいバージョンで再起動します")
	os.Setenv(updatedEnv, "1")
	err = restart(exe)
	log.Println("再起動に失敗しました", err)
	log.Println("手動で再起動してください")
	time.Sleep(5 * time.Second)
	os.Exit(1)
}
