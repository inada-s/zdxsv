package main

import (
	"log"
	"os"
	"time"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

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
	up, err := selfupdate.NewUpdater(selfupdate.Config{Filters: []string{`^zdxsv_`}})
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
	if err := selfupdate.UpdateTo(latest.AssetURL, exe); err != nil {
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
