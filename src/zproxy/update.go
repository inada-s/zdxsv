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
	releaseVersion = "0.0.0"
	releaseCommit  = "local"
	releaseDate    = "local"
)

func printReleaseInfo() {
	log.Println("releaseVersion", releaseVersion)
	log.Println("releaseCommit", releaseCommit)
	log.Println("releaseDate", releaseDate)
}

func doSelfUpdate() {
	log.Println("アップデートチェックを行います")

	latest, found, err := selfupdate.DetectLatest("inada-s/zdxsv")
	if err != nil {
		log.Println("アップデートチェックに失敗しました", err)
	}

	log.Println("最新版>", latest.Version)

	v := semver.MustParse(releaseVersion)
	if !found || latest.Version.LTE(v) {
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
	log.Println("再起動してください")
	time.Sleep(time.Second)
	os.Exit(0)
}
