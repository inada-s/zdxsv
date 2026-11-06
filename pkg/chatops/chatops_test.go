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
		"other game": {{"@m_rule"}, {"anything"}, {"1"}},
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
	r.sheets.sheets = banSheets()
	if msg = r.run("ops-user"); msg != "m_ban updated: 0 rows" || banned(t, c) {
		t.Errorf("empty table: %q %v", msg, masterBans(t))
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
