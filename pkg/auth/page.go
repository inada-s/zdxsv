package auth

import (
	"html/template"
	"net/http"
	"strings"
)

type page struct {
	Lang          string
	Title         string
	Notice        []string
	Button        string
	ProviderURL   string
	Message       string
	Expired       string
	Denied        string
	ProviderError string
}

var pages = map[string]page{
	"ja": {
		Lang:  "ja",
		Title: "ZDXSV サインイン",
		Notice: []string{
			"Discord アカウントでサインインします。ZDXSV が保存するのは Discord のユーザー ID だけです。",
			"サインインが終わったらエミュレーターに戻ってください。",
		},
		Button:        "Discord でサインイン",
		Expired:       "サインインの期限が切れました。エミュレーターからやり直してください。",
		Denied:        "サインインが取り消されました。",
		ProviderError: "サインインに失敗しました。しばらくしてからやり直してください。",
	},
	"en": {
		Lang:  "en",
		Title: "ZDXSV sign in",
		Notice: []string{
			"Sign in with your Discord account. ZDXSV stores only your Discord user ID.",
			"When the sign-in is done, go back to the emulator.",
		},
		Button:        "Sign in with Discord",
		Expired:       "The sign-in expired. Start again from the emulator.",
		Denied:        "The sign-in was cancelled.",
		ProviderError: "The sign-in failed. Try again later.",
	},
	"zh-TW": {
		Lang:  "zh-TW",
		Title: "ZDXSV 登入",
		Notice: []string{
			"使用 Discord 帳號登入。ZDXSV 只會儲存您的 Discord 使用者 ID。",
			"登入完成後，請回到模擬器。",
		},
		Button:        "使用 Discord 登入",
		Expired:       "登入已逾時，請從模擬器重新開始。",
		Denied:        "登入已取消。",
		ProviderError: "登入失敗，請稍後再試。",
	},
}

func pickLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); l != "" {
		if _, ok := pages[l]; ok {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "ja"):
			return "ja"
		case strings.HasPrefix(tag, "zh"):
			return "zh-TW"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "ja"
}

func pageText(lang string) page {
	return pages[lang]
}

const head = `<!DOCTYPE html><html lang="{{.Lang}}"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title>
<style>body{font-family:sans-serif;max-width:36em;margin:3em auto;padding:0 1em;line-height:1.6}
a.b{display:inline-block;background:#5865f2;color:#fff;padding:.7em 1.4em;border-radius:6px;text-decoration:none}</style>
</head><body><h1>{{.Title}}</h1>`

var tplStart = template.Must(template.New("start").Parse(head +
	`{{range .Notice}}<p>{{.}}</p>{{end}}
<p><a class="b" href="{{.ProviderURL}}">{{.Button}}</a></p></body></html>`))

var tplMessage = template.Must(template.New("message").Parse(head + `<p>{{.Message}}</p></body></html>`))
