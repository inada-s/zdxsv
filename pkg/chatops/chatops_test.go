package chatops

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
)

type fakeSheets struct {
	mu     sync.Mutex
	sheets map[string][][]string
}

func (f *fakeSheets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/v4/spreadsheets/sheet-id") {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/values:batchGet") {
		var vrs []map[string]interface{}
		for _, rg := range r.URL.Query()["ranges"] {
			title := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(rg, "'!"+sheetRange), "'"), "''", "'")
			vrs = append(vrs, map[string]interface{}{"range": rg, "values": f.sheets[title]})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"valueRanges": vrs})
		return
	}
	var sheets []interface{}
	for title := range f.sheets {
		sheets = append(sheets, map[string]interface{}{"properties": map[string]string{"title": title}})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"sheets": sheets})
}

type fakeDiscordAPI struct {
	edits chan string
}

func (f *fakeDiscordAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch || r.URL.Path != "/webhooks/app-id/itoken/messages/@original" {
		http.NotFound(w, r)
		return
	}
	var m struct{ Content string }
	json.NewDecoder(r.Body).Decode(&m)
	f.edits <- m.Content
}

type rig struct {
	t      *testing.T
	key    ed25519.PrivateKey
	api    *httptest.Server
	sheets *fakeSheets
	edits  chan string
	d      *Discord
}

func newRig(t *testing.T) *rig {
	dbtest.Use(t)
	pub, key, _ := ed25519.GenerateKey(nil)
	sheets := &fakeSheets{}
	ss := httptest.NewServer(sheets)
	t.Cleanup(ss.Close)
	dapi := &fakeDiscordAPI{edits: make(chan string, 4)}
	ds := httptest.NewServer(dapi)
	t.Cleanup(ds.Close)
	updater := &MasterUpdater{Sheet: &Spreadsheet{ID: "sheet-id", BaseURL: ss.URL}}
	d := &Discord{
		PublicKey: pub,
		Allow:     map[string]bool{"ops-role": true, "ops-user": true},
		Commands:  MasterCommands(updater.Sheet.(*Spreadsheet), updater),
		APIURL:    ds.URL,
	}
	api := httptest.NewServer(d)
	t.Cleanup(api.Close)
	return &rig{t: t, key: key, api: api, sheets: sheets, edits: dapi.edits, d: d}
}

func (r *rig) post(body string, sign bool) (int, map[string]interface{}) {
	r.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, r.api.URL, bytes.NewBufferString(body))
	ts := "1700000000"
	sig := ed25519.Sign(r.key, []byte(ts+body))
	if !sign {
		sig[0] ^= 1
	}
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return res.StatusCode, m
}

func command(name, user string, roles ...string) string {
	data := map[string]interface{}{"name": name}
	if cmd, sub, ok := strings.Cut(name, " "); ok {
		data = map[string]interface{}{"name": cmd, "options": []map[string]interface{}{{"name": sub, "type": 1}}}
	}
	b, _ := json.Marshal(map[string]interface{}{
		"type": 2, "application_id": "app-id", "token": "itoken",
		"data":   data,
		"member": map[string]interface{}{"user": map[string]string{"id": user}, "roles": roles},
	})
	return string(b)
}

func (r *rig) run(user string, roles ...string) string {
	return r.runCommand("op master-up", user, roles...)
}

func (r *rig) runCommand(name, user string, roles ...string) string {
	r.t.Helper()
	code, m := r.post(command(name, user, roles...), true)
	if code != http.StatusOK || m["type"] != float64(responseDeferredChannelMessage) {
		r.t.Fatalf("answer %d %v, want deferred message", code, m)
	}
	r.d.Wait()
	select {
	case s := <-r.edits:
		return s
	default:
		r.t.Fatal("no message edit")
		return ""
	}
}

func masterBans(t *testing.T) []*db.MasterBan {
	t.Helper()
	b, err := db.DefaultDB.GetMasterBans()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newUser(t *testing.T) *db.User {
	t.Helper()
	a, err := db.DefaultDB.RegisterAccount("1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.DefaultDB.RegisterUser(a.LoginKey)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func banned(t *testing.T, u *db.User) bool {
	t.Helper()
	b, err := db.DefaultDB.FindBan(u.LoginKey)
	if err != nil {
		t.Fatal(err)
	}
	return b != nil
}

func banSheets(rows ...[]string) map[string][][]string {
	table := [][]string{{"", "@m_ban"}, {"", "key", "until", "reason", "", "note"}}
	for _, r := range rows {
		table = append(table, append([]string{""}, r...))
	}
	return map[string][][]string{
		"memo":       {{"free text"}},
		"other game": {{"@m_patch"}, {"anything"}, {"1"}},
		"ban":        append(table, []string{}, []string{"", "trailing rows are not part of the table"}),
	}
}

func TestReloadMasterBan(t *testing.T) {
	r := newRig(t)
	a, b, c := newUser(t), newUser(t), newUser(t)
	sibling, err := db.DefaultDB.RegisterUser(a.LoginKey)
	if err != nil {
		t.Fatal(err)
	}
	r.sheets.sheets = banSheets(
		[]string{a.UserID, "2099/12/31 23:59:59", "cheat"},
		[]string{b.UserID, "2000-01-01"},
	)
	msg := r.run("someone", "ops-role")
	if msg != "m_ban updated: 2 rows" {
		t.Fatalf("message %q", msg)
	}
	got := masterBans(t)
	if len(got) != 2 {
		t.Fatalf("m_ban %+v", got)
	}
	byKey := map[string]*db.MasterBan{got[0].Key: got[0], got[1].Key: got[1]}
	if x := byKey[a.UserID]; x == nil || !x.Until.Equal(time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC)) || x.Reason != "cheat" {
		t.Errorf("row a %+v", x)
	}
	if !banned(t, a) || !banned(t, sibling) || banned(t, b) || banned(t, c) {
		t.Errorf("after the first load: a %v sibling %v b(expired) %v c %v", banned(t, a), banned(t, sibling), banned(t, b), banned(t, c))
	}

	r.sheets.sheets = banSheets(
		[]string{c.UserID, "2099-01-01 00:00"},
		[]string{"NOSUCH", "2099-01-01"},
		[]string{c.UserID, "tomorrow"},
		[]string{"", "2099-01-01"},
	)
	msg = r.run("ops-user")
	for _, s := range []string{"failed, nothing changed", `row 2: key "NOSUCH" is no user`, `row 3: key "` + c.UserID + `" is also on row 1`,
		`row 3: until "tomorrow" is not a time`, `row 4: key "" is no user`} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
	if len(masterBans(t)) != 2 || banned(t, c) || !banned(t, a) {
		t.Fatalf("an invalid sheet changed m_ban: %+v", masterBans(t))
	}

	r.sheets.sheets = map[string][][]string{"s": {{"@m_ban"}, {"key", "until", "lift"}}}
	if msg = r.run("ops-user"); !strings.Contains(msg, `unknown column "lift"`) {
		t.Errorf("message %q", msg)
	}
	r.sheets.sheets = map[string][][]string{"s": {{"@m_ban"}, {"key", "reason"}}}
	if msg = r.run("ops-user"); !strings.Contains(msg, "no column until") {
		t.Errorf("message %q", msg)
	}
	r.sheets.sheets = map[string][][]string{"s": {{"x"}}}
	if msg = r.run("ops-user"); !strings.Contains(msg, "no table @m_ban") {
		t.Errorf("message %q", msg)
	}
	if len(masterBans(t)) != 2 {
		t.Fatalf("m_ban changed: %+v", masterBans(t))
	}

	r.sheets.sheets = banSheets([]string{c.UserID, "2099-01-01T09:00:00+09:00"})
	if msg = r.run("ops-user"); msg != "m_ban updated: 1 rows" || banned(t, a) || !banned(t, c) {
		t.Errorf("replace: %q a %v c %v", msg, banned(t, a), banned(t, c))
	}
	if got := masterBans(t); !got[0].Until.Equal(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("until with an offset: %v", got[0].Until)
	}
	r.sheets.sheets = map[string][][]string{"s": {{"@m_ban"}, {"#memo", "key", "#", "until", "#memo"}, {"x", b.UserID, "y", "2099-01-01", "z"}}}
	if msg = r.run("ops-user"); msg != "m_ban updated: 1 rows" || !banned(t, b) || banned(t, c) {
		t.Errorf("# columns: %q", msg)
	}
	r.sheets.sheets = banSheets()
	if msg = r.run("ops-user"); msg != "m_ban updated: 0 rows" || banned(t, c) {
		t.Errorf("empty table: %q %v", msg, masterBans(t))
	}
}

func TestReloadServerSetting(t *testing.T) {
	r := newRig(t)
	setting := func(key string) string {
		t.Helper()
		v, err := db.DefaultDB.GetServerSetting(key)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	withSettings := func(rows ...[]string) map[string][][]string {
		s := banSheets()
		s["setting"] = append([][]string{{"@m_server_setting"}, {"key", "value"}}, rows...)
		return s
	}

	r.sheets.sheets = withSettings([]string{"min_pcsx2_version", "zdxsv-1.2.0"}, []string{"pcsx2_allow", "k1,k2"})
	if msg := r.run("ops-user"); msg != "m_ban updated: 0 rows, m_server_setting: 2 rows" {
		t.Fatalf("message %q", msg)
	}
	if setting("min_pcsx2_version") != "zdxsv-1.2.0" || setting("pcsx2_allow") != "k1,k2" {
		t.Fatalf("after load: %q %q", setting("min_pcsx2_version"), setting("pcsx2_allow"))
	}

	r.sheets.sheets = withSettings([]string{"min_pcsx2_version", "1.3"}, []string{"min_pcsx2_version", "zdxsv-1.3.0"},
		[]string{"", "x"})
	msg := r.run("ops-user")
	for _, s := range []string{"failed, nothing changed", `row 1: min_pcsx2_version "1.3" is not a version`,
		`row 2: key "min_pcsx2_version" is also on row 1`, "row 3: no key"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
	if setting("min_pcsx2_version") != "zdxsv-1.2.0" {
		t.Fatalf("an invalid sheet changed m_server_setting: %q", setting("min_pcsx2_version"))
	}

	r.sheets.sheets = banSheets()
	if msg := r.run("ops-user"); msg != "m_ban updated: 0 rows" || setting("min_pcsx2_version") != "zdxsv-1.2.0" {
		t.Fatalf("no table: %q %q", msg, setting("min_pcsx2_version"))
	}

	r.sheets.sheets = withSettings([]string{"min_pcsx2_version", ""})
	if msg := r.run("ops-user"); msg != "m_ban updated: 0 rows, m_server_setting: 1 rows" ||
		setting("min_pcsx2_version") != "" || setting("pcsx2_allow") != "" {
		t.Fatalf("replace: %q %q %q", msg, setting("min_pcsx2_version"), setting("pcsx2_allow"))
	}
}

func TestParseLobbyHack(t *testing.T) {
	for _, c := range []struct {
		cell, want string
		ok         bool
	}{
		{"", "", true},
		{"none", "none", true},
		{"zproxy_test, chat_command", "zproxy_test,chat_command", true},
		{"widescreen,chat_command", "widescreen,chat_command", true},
		{"pad_delay,widescreen", "", false},
		{"none,chat_command", "", false},
		{"chat_command,chat_command", "", false},
		{"chat_command,wide", "", false},
	} {
		got, err := parseLobbyHack(c.cell)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("%q: %q, %v", c.cell, got, err)
		}
	}
}

func TestReloadLobbySetting(t *testing.T) {
	r := newRig(t)
	lobby := func(platform string, no int) *db.LobbySetting {
		t.Helper()
		s, err := db.DefaultDB.GetLobbySetting(platform, no)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	rule := func(id string) *db.Rule {
		t.Helper()
		r, err := db.DefaultDB.GetRule(id)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	ruleHeader := []string{"id", "battle_gauge", "battle_time", "reload_type"}
	lobbyHeader := []string{"platform", "no", "start", "explain", "rule_id", "hack"}
	sheets := func(rules, lobbies [][]string) map[string][][]string {
		s := banSheets()
		if rules != nil {
			s["rule"] = append([][]string{{"@m_rule"}}, rules...)
		}
		if lobbies != nil {
			s["lobby"] = append([][]string{{"@m_lobby_setting"}}, lobbies...)
		}
		return s
	}

	r.sheets.sheets = sheets(
		[][]string{ruleHeader, {"tai", "1", "90", ""}, {"noreload", "", "", "1"}},
		[][]string{lobbyHeader, {"emu", "5", "1v1", "タイマン", "tai", ""}, {"emu", "4", "", "", "", "none"}, {"console", "5", "", "", "noreload", "widescreen"}})
	if msg := r.run("ops-user"); msg != "m_ban updated: 0 rows, m_lobby_setting: 3 rows, m_rule: 2 rows" {
		t.Fatalf("message %q", msg)
	}
	if s := lobby("emu", 5); s == nil || s.Start != "1v1" || s.Explain != "タイマン" || s.RuleID != "tai" || s.Hack != "" {
		t.Fatalf("emu 5: %+v", s)
	}
	if s := lobby("emu", 4); s == nil || s.Hack != "none" || s.RuleID != "" {
		t.Fatalf("emu 4: %+v", s)
	}
	if s := lobby("console", 5); s == nil || s.RuleID != "noreload" || s.Hack != "widescreen" {
		t.Fatalf("console 5: %+v", s)
	}
	want := db.Rule{ID: "tai", BattleCount: 0, VisibleTeam: 0, BattleTime: 90, BattleGauge: 1, ReloadType: 0, DamageLevel: 3, AwakeType: 0, AwakeIncRatio: 100, AwakeDecRatio: 100}
	if got := rule("tai"); got == nil || *got != want {
		t.Fatalf("rule tai %+v, want %+v", got, want)
	}
	if got := rule("noreload"); got == nil || got.ReloadType != 1 || got.BattleGauge != 600 || got.BattleTime != 210 {
		t.Fatalf("rule noreload %+v", got)
	}

	r.sheets.sheets = sheets(
		[][]string{ruleHeader, {"", "70000", "", "x"}, {"tai", "", "", ""}, {"tai", "", "", ""}},
		[][]string{lobbyHeader, {"pc", "26", "3v3", "", "", "wide"}, {"emu", "1", "", "", "", ""}, {"emu", "1", "", "", "", ""}})
	msg := r.run("ops-user")
	for _, s := range []string{"failed, nothing changed", "row 1: no id", `row 1: battle_gauge "70000" is out of range`,
		`row 1: reload_type "x"`, `row 3: id "tai" is also on row 2`} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
	r.sheets.sheets = sheets(nil,
		[][]string{lobbyHeader, {"pc", "26", "3v3", "", "", "wide"}, {"emu", "1", "", "", "", ""}, {"emu", "1", "", "", "", ""}})
	msg = r.run("ops-user")
	for _, s := range []string{"failed, nothing changed", `row 1: platform "pc"`, `row 1: no "26" is not a lobby number 0-25`,
		`row 1: start "3v3"`, `row 1: hack "wide" is not one of [none pad_delay widescreen zproxy_test chat_command]`, "row 3: lobby emu 1 is also on row 2"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
	if lobby("emu", 5) == nil || lobby("emu", 1) != nil || rule("tai") == nil {
		t.Fatal("an invalid sheet changed the master")
	}
	r.sheets.sheets = sheets([][]string{{"id", "max_players"}}, nil)
	if msg := r.run("ops-user"); !strings.Contains(msg, `unknown column "max_players"`) {
		t.Fatalf("unknown column: %q", msg)
	}

	r.sheets.sheets = sheets(nil, [][]string{lobbyHeader, {"emu", "6", "", "", "nosuch", ""}})
	if msg := r.run("ops-user"); !strings.Contains(msg, `lobby emu 6: rule_id "nosuch" is not in m_rule`) || lobby("emu", 6) != nil {
		t.Fatalf("unknown rule_id against the DB: %q", msg)
	}
	r.sheets.sheets = sheets([][]string{ruleHeader, {"other", "", "", ""}}, nil)
	if msg := r.run("ops-user"); !strings.Contains(msg, `lobby emu 5: rule_id "tai" is not in m_rule`) || rule("tai") == nil {
		t.Fatalf("m_rule dropping a rule in use: %q", msg)
	}

	r.sheets.sheets = banSheets()
	if msg := r.run("ops-user"); msg != "m_ban updated: 0 rows" || lobby("emu", 5) == nil || rule("tai") == nil {
		t.Fatalf("no table: %q", msg)
	}

	r.sheets.sheets = sheets([][]string{ruleHeader}, [][]string{lobbyHeader})
	if msg := r.run("ops-user"); msg != "m_ban updated: 0 rows, m_lobby_setting: 0 rows, m_rule: 0 rows" || lobby("emu", 5) != nil || rule("tai") != nil {
		t.Fatalf("empty table: %q", msg)
	}
}

func TestOpMaster(t *testing.T) {
	r := newRig(t)
	if got := r.runCommand("op master", "someone", "ops-role"); got != "https://docs.google.com/spreadsheets/d/sheet-id" {
		t.Fatalf("op master: %q", got)
	}
}

func TestDiscordRejects(t *testing.T) {
	r := newRig(t)
	u := newUser(t)
	r.sheets.sheets = banSheets([]string{u.UserID, "2099-01-01"})

	if code, _ := r.post(command("op master-up", "ops-user"), false); code != http.StatusUnauthorized {
		t.Errorf("bad signature: %d", code)
	}
	if code, m := r.post(`{"type":1}`, true); code != http.StatusOK || m["type"] != float64(responsePong) {
		t.Errorf("ping: %d %v", code, m)
	}
	for _, c := range []struct{ body, msg string }{
		{command("op master-up", "someone", "other-role"), "not allowed"},
		{command("op other", "ops-user"), "unknown command op other"},
		{command("op", "ops-user"), "unknown command op"},
	} {
		code, m := r.post(c.body, true)
		data, _ := m["data"].(map[string]interface{})
		if code != http.StatusOK || m["type"] != float64(responseChannelMessage) || data["content"] != c.msg || data["flags"] != float64(messageFlagEphemeral) {
			t.Errorf("%s: %d %v", c.msg, code, m)
		}
	}
	r.d.Wait()
	if len(r.edits) != 0 || len(masterBans(t)) != 0 || banned(t, u) {
		t.Errorf("a rejected command ran")
	}
}
