package main

import (
	"log"
	"os"
	"time"

	"github.com/equinox-io/equinox"
)

const appID = "app_4Lz3DqAf1d4"

var publicKey = []byte(`
-----BEGIN ECDSA PUBLIC KEY-----
MHYwEAYHKoZIzj0CAQYFK4EEACIDYgAECnv0D106fXZ7iVzcRCSjHO15EO5sBBeY
Y1pqovBC52/yJZguNhq3U7oMmbVzpbpYwnA/iVAKUvUghdn6mfFNip7vTezhrFKx
2mVbrtlGuM/NRDsP7wpYYa5V6e31YmW7
-----END ECDSA PUBLIC KEY-----
`)

func equinoxUpdate() error {
	log.Println("アップデートチェックを行います")
	var opts equinox.Options
	if err := opts.SetPublicKeyPEM(publicKey); err != nil {
		log.Println("鍵エラー")
		return err
	}

	// check for the update
	resp, err := equinox.Check(appID, opts)
	switch {
	case err == equinox.NotAvailableErr:
		log.Println("既に最新版です")
		return nil
	case err != nil:
		log.Println("アップデートチェックに失敗しました")
		return err
	}

	// fetch the update and apply it
	log.Println("アップデート中...")
	err = resp.Apply()
	if err != nil {
		log.Println("アップデートに失敗しました")
		return err
	}

	log.Println("アップデートに成功しました")
	log.Println("再起動してください")
	time.Sleep(time.Second)
	os.Exit(0)
	return nil
}

