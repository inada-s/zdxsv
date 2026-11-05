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
	CarryNotice   string
	Button        string
	ProviderURL   string
	Carry         bool
	Message       string
	Expired       string
	Denied        string
	ProviderError string
	CarryBad      string
	CarryLocked   string
	IdentityTaken string
	AccountLinked string
}

var pages = map[string]page{
	"ja": {
		Lang:  "ja",
		Title: "ZDXSV サインイン",
		Notice: []string{
			"Discord アカウントでサインインします。ZDXSV が受け取るのは Discord のユーザー ID だけです。",
			"サインイン後、エミュレーターに戻ると自動でログインします。",
			"ひとつの Discord アカウントにつき ZDXSV アカウントはひとつです。",
		},
		CarryNotice:   "引き継ぎコードのアカウントをこの Discord アカウントに結び付けます。",
		Button:        "Discord でサインイン",
		Expired:       "サインインの期限が切れました。エミュレーターからやり直してください。",
		Denied:        "サインインが取り消されました。",
		ProviderError: "サインインに失敗しました。しばらくしてからやり直してください。",
		CarryBad:      "引き継ぎコードが正しくないか、期限切れです。ロビーで新しいコードを発行してください。",
		CarryLocked:   "引き継ぎコードの失敗が多すぎます。しばらくしてからやり直してください。",
		IdentityTaken: "この Discord アカウントには別の ZDXSV アカウントが結び付いています。",
		AccountLinked: "このアカウントはすでに Discord アカウントに結び付いています。",
	},
	"en": {
		Lang:  "en",
		Title: "ZDXSV sign in",
		Notice: []string{
			"Sign in with your Discord account. ZDXSV receives only your Discord user ID.",
			"After signing in, go back to the emulator: it logs in by itself.",
			"One Discord account has one ZDXSV account.",
		},
		CarryNotice:   "The account of your carry-over code will be linked to this Discord account.",
		Button:        "Sign in with Discord",
		Expired:       "The sign-in expired. Start again from the emulator.",
		Denied:        "The sign-in was cancelled.",
		ProviderError: "The sign-in failed. Try again later.",
		CarryBad:      "The carry-over code is wrong or expired. Get a new one in the lobby.",
		CarryLocked:   "Too many wrong carry-over codes. Try again later.",
		IdentityTaken: "This Discord account already has another ZDXSV account.",
		AccountLinked: "This account is already linked to a Discord account.",
	},
	"zh-TW": {
		Lang:  "zh-TW",
		Title: "ZDXSV 登入",
		Notice: []string{
			"使用 Discord 帳號登入。ZDXSV 只會取得您的 Discord 使用者 ID。",
			"登入後回到模擬器，即會自動登入。",
			"一個 Discord 帳號對應一個 ZDXSV 帳號。",
		},
		CarryNotice:   "引繼碼的帳號將與此 Discord 帳號連結。",
		Button:        "使用 Discord 登入",
		Expired:       "登入已逾時，請從模擬器重新開始。",
		Denied:        "登入已取消。",
		ProviderError: "登入失敗，請稍後再試。",
		CarryBad:      "引繼碼錯誤或已過期，請在大廳重新取得。",
		CarryLocked:   "引繼碼錯誤次數過多，請稍後再試。",
		IdentityTaken: "此 Discord 帳號已連結其他 ZDXSV 帳號。",
		AccountLinked: "此帳號已連結 Discord 帳號。",
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
	`{{range .Notice}}<p>{{.}}</p>{{end}}{{if .Carry}}<p><b>{{.CarryNotice}}</b></p>{{end}}
<p><a class="b" href="{{.ProviderURL}}">{{.Button}}</a></p></body></html>`))

var tplMessage = template.Must(template.New("message").Parse(head + `<p>{{.Message}}</p></body></html>`))
