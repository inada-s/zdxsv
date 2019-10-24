package main

import (
	"log"
	"os"
	"time"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

const releaseVersion = "0.1.0"

func doSelfUpdate() {
	log.Println("アップデートチェックを行います")

	latest, found, err := selfupdate.DetectLatest("inada-s/zdxsv")
	if err != nil {
		log.Println("アップデートチェックに失敗しました", err)
	}

	log.Println(latest, found, err)
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
	time.Sleep(2 * time.Second)
	os.Exit(0)
}
