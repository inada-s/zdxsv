package chatops

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

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
	updater := &LobbySettingUpdater{Sheet: &Spreadsheet{ID: "sheet-id", BaseURL: ss.URL}}
	d := &Discord{
		PublicKey: pub,
		Allow:     map[string]bool{"ops-role": true, "ops-user": true},
		Commands:  map[string]func(context.Context) (string, error){"reload-lobby-setting": updater.Update},
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
	b, _ := json.Marshal(map[string]interface{}{
		"type": 2, "application_id": "app-id", "token": "itoken",
		"data":   map[string]string{"name": name},
		"member": map[string]interface{}{"user": map[string]string{"id": user}, "roles": roles},
	})
	return string(b)
}

func (r *rig) run(user string, roles ...string) string {
	r.t.Helper()
	code, m := r.post(command("reload-lobby-setting", user, roles...), true)
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

func lobbySettings(t *testing.T) []*db.LobbySetting {
	t.Helper()
	s, err := db.DefaultDB.GetLobbySettings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var goodSheets = map[string][][]string{
	"memo": {{"free text"}},
	"lobby's settings": {
		{"", "@m_lobby_setting"},
		{"", "platform", "lobby_id", "battle_gauge", "battle_time", "battle_count", "", "note"},
		{"", "console", "2", "600", "210", "3"},
		{"", "emu", "2"},
		{"", "emu", "5", "1", "", "1", "", "ignored"},
		{},
		{"", "trailing rows are not part of the table"},
	},
}

func TestReloadLobbySetting(t *testing.T) {
	r := newRig(t)
	r.sheets.sheets = goodSheets
	msg := r.run("someone", "ops-role")
	if msg != "m_lobby_setting updated: 3 rows" {
		t.Fatalf("message %q", msg)
	}
	want := []*db.LobbySetting{
		{Platform: "console", LobbyID: 2, BattleGauge: 600, BattleTime: 210, BattleCount: 3},
		{Platform: "emu", LobbyID: 2},
		{Platform: "emu", LobbyID: 5, BattleGauge: 1, BattleCount: 1},
	}
	if got := lobbySettings(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("m_lobby_setting %+v", got)
	}

	r.sheets.sheets = map[string][][]string{"s": {
		{"@m_lobby_setting"},
		{"platform", "lobby_id", "battle_gauge", "battle_time"},
		{"console", "1", "600"},
		{"ps2", "26", "123", "x"},
		{"console", "1"},
	}}
	msg = r.run("ops-user")
	for _, s := range []string{"failed, nothing changed", `row 2: battle_gauge "123"`, `row 2: battle_time "x"`,
		"row 2: lobby_id 26", `row 2: platform "ps2"`, "row 3: console lobby 1 is also on row 1"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
	if got := lobbySettings(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("an invalid sheet changed m_lobby_setting: %+v", got)
	}

	r.sheets.sheets = map[string][][]string{"s": {{"@m_lobby_setting"}, {"platform", "lobby", "battle_time"}}}
	if msg = r.run("ops-user"); !strings.Contains(msg, `unknown column "lobby"`) || !strings.Contains(msg, "no column lobby_id") {
		t.Errorf("message %q", msg)
	}
	r.sheets.sheets = map[string][][]string{"s": {{"x"}}}
	if msg = r.run("ops-user"); !strings.Contains(msg, "no table @m_lobby_setting") {
		t.Errorf("message %q", msg)
	}
	if got := lobbySettings(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("m_lobby_setting changed: %+v", got)
	}

	r.sheets.sheets = map[string][][]string{"s": {{"@m_lobby_setting"}, {"platform", "lobby_id"}}}
	if msg = r.run("ops-user"); msg != "m_lobby_setting updated: 0 rows" || len(lobbySettings(t)) != 0 {
		t.Errorf("empty table: %q %v", msg, lobbySettings(t))
	}
}

func TestDiscordRejects(t *testing.T) {
	r := newRig(t)
	r.sheets.sheets = goodSheets

	if code, _ := r.post(command("reload-lobby-setting", "ops-user"), false); code != http.StatusUnauthorized {
		t.Errorf("bad signature: %d", code)
	}
	if code, m := r.post(`{"type":1}`, true); code != http.StatusOK || m["type"] != float64(responsePong) {
		t.Errorf("ping: %d %v", code, m)
	}
	for _, c := range []struct{ body, msg string }{
		{command("reload-lobby-setting", "someone", "other-role"), "not allowed"},
		{command("other", "ops-user"), "unknown command other"},
	} {
		code, m := r.post(c.body, true)
		data, _ := m["data"].(map[string]interface{})
		if code != http.StatusOK || m["type"] != float64(responseChannelMessage) || data["content"] != c.msg || data["flags"] != float64(messageFlagEphemeral) {
			t.Errorf("%s: %d %v", c.msg, code, m)
		}
	}
	r.d.Wait()
	if len(r.edits) != 0 || len(lobbySettings(t)) != 0 {
		t.Errorf("a rejected command ran")
	}
}
