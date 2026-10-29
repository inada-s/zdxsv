package lobby

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
)

func TestLobbyBan(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	ops := func(path string, q url.Values) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		(&OpsHandler{}).ServeHTTP(w, httptest.NewRequest("GET", path+"?"+q.Encode(), nil))
		return w.Code, w.Body.String()
	}
	mustOps := func(path string, q url.Values) {
		t.Helper()
		if code, body := ops(path, q); code != 200 {
			t.Fatalf("%s %v: %d %q", path, q, code, body)
		}
	}
	dial := func(machineID string) *testClient {
		c := dialLobby(t, addr)
		if machineID != "" {
			n := NewClientNotice(0x9950)
			n.Writer().Write([]byte("emulator=pcsx2\nmachine_id=" + machineID + "\n"))
			c.send(n)
		}
		return c
	}
	refused := func(ac *db.Account, machineID string) {
		t.Helper()
		c := dial(machineID)
		kp := c.expect(0x6101, CategoryQuestion)
		a := NewClientAnswer(kp)
		a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
		c.send(a)
		if msg := c.expect(0x6003, CategoryNotice).Reader().ReadString(); !strings.Contains(msg, "BANNED") {
			t.Fatalf("refusal %q", msg)
		}
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6103 {
				t.Fatal("first data asked after the refusal")
			}
		}
	}

	ac1 := registerAccount(t)
	u1, _, _ := dial("m1").login(ac1, "******", "one")

	code, body := ops("/ops/accounts", url.Values{"machine_id": {"m1"}})
	var accounts []struct {
		LoginKey           string `json:"login_key"`
		LastLoginIP        string `json:"last_login_ip"`
		LastLoginMachineID string `json:"last_login_machine_id"`
		SessionID          string `json:"session_id"`
		Users              []struct {
			UserID string `json:"user_id"`
		} `json:"users"`
	}
	if err := json.Unmarshal([]byte(body), &accounts); code != 200 || err != nil {
		t.Fatalf("accounts: %d %q %v", code, body, err)
	}
	if len(accounts) != 1 || accounts[0].LoginKey != ac1.LoginKey || accounts[0].LastLoginIP != "127.0.0.1" ||
		accounts[0].LastLoginMachineID != "m1" || accounts[0].SessionID != "" ||
		len(accounts[0].Users) != 1 || accounts[0].Users[0].UserID != u1 {
		t.Fatalf("accounts: %s", body)
	}

	for _, q := range []url.Values{{"kind": {"x"}, "value": {"m1"}}, {"kind": {"machine"}}} {
		if code, _ := ops("/ops/ban", q); code != 400 {
			t.Errorf("ban %v: %d", q, code)
		}
	}

	ac2 := registerAccount(t)
	mustOps("/ops/ban", url.Values{"kind": {"machine"}, "value": {"m1"}, "reason": {"cheat"}})
	refused(ac2, "m1")
	dial("m2").login(ac2, "******", "two")

	code, body = ops("/ops/bans", nil)
	var bans []db.Ban
	if err := json.Unmarshal([]byte(body), &bans); code != 200 || err != nil || len(bans) != 1 ||
		bans[0].Kind != "machine" || bans[0].Value != "m1" || bans[0].Reason != "cheat" {
		t.Fatalf("bans: %d %s %v", code, body, err)
	}

	mustOps("/ops/unban", url.Values{"kind": {"machine"}, "value": {"m1"}})
	if code, _ := ops("/ops/unban", url.Values{"kind": {"machine"}, "value": {"m1"}}); code != 404 {
		t.Errorf("second unban: %d", code)
	}
	dial("m1").login(ac2, "******", "two")

	mustOps("/ops/ban", url.Values{"kind": {"user"}, "value": {u1}})
	refused(ac1, "")
	dial("").login(ac2, "******", "two")
	mustOps("/ops/unban", url.Values{"kind": {"user"}, "value": {u1}})

	mustOps("/ops/ban", url.Values{"kind": {"ip"}, "value": {"127.0.0.1"}})
	refused(ac2, "m2")
	mustOps("/ops/unban", url.Values{"kind": {"ip"}, "value": {"127.0.0.1"}})
	dial("").login(ac1, u1, "one")
}
