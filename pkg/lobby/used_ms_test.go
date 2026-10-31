package lobby

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

// A battle member's report round_data sets every player's used_ms; /lbs/replay?used_ms= finds it.
func TestReportUsedMsSearch(t *testing.T) {
	dbtest.Use(t)
	for _, r := range []*db.BattleRecord{
		{BattleCode: "B1", UserID: "AAAAAA", Players: 2, Pos: 1, Side: 1},
		{BattleCode: "B1", UserID: "BBBBBB", Players: 2, Pos: 2, Side: 2},
		{BattleCode: "B2", UserID: "AAAAAA", Players: 2, Pos: 1, Side: 1},
		{BattleCode: "B2", UserID: "CCCCCC", Players: 2, Pos: 2, Side: 2},
	} {
		if err := db.DefaultDB.AddBattleRecord(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{"B1", "B2"} {
		if err := db.DefaultDB.SetReplayURL(code, "https://example.com/"+code+".pb"); err != nil {
			t.Fatal(err)
		}
	}

	report := func(sender, code string, rounds ...[]int32) {
		r := &proto.P2PMatchingReport{BattleCode: pb.String(code), UserId: pb.String(sender)}
		for _, ms := range rounds {
			r.RoundData = append(r.RoundData, &proto.BattleLogRound{WinTeam: pb.Int32(1), UsedMs: ms})
		}
		body, err := pb.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		p := &AppPeer{PlatformInfo: map[string]string{"emulator": "pcsx2"}}
		p.UserID = sender
		m := NewClientNotice(0x9952)
		m.Writer().Write(body)
		defaultHandlers.handlers[0x9952](p, m)
	}
	report("BBBBBB", "B1", []int32{1, 9}, []int32{4, 9}, []int32{1})
	report("CCCCCC", "B1", []int32{30, 30})
	report("AAAAAA", "B2", []int32{2, 64})

	h := &APIHandler{}
	search := func(query string) (codes []string, users map[string]string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/replay?"+query, nil))
		var found []*db.FoundReplay
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &found); err != nil {
				t.Fatal(err)
			}
		}
		users = map[string]string{}
		for _, f := range found {
			codes = append(codes, f.BattleCode)
			for _, u := range f.Users {
				users[f.BattleCode+"/"+u.UserID] = u.UsedMsList
			}
		}
		return codes, users
	}

	_, users := search("")
	want := map[string]string{"B1/AAAAAA": "1,4,1", "B1/BBBBBB": "9,9,0", "B2/AAAAAA": "2", "B2/CCCCCC": "0"}
	if !reflect.DeepEqual(want, users) {
		t.Errorf("used_ms_list %v, want %v", users, want)
	}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"used_ms=4", []string{"B1"}},
		{"used_ms=9", []string{"B1"}},
		{"used_ms=30", nil},
		{"used_ms=64", nil},
		{"used_ms=2", []string{"B2"}},
		{"used_ms=9&user_id=AAAAAA", nil},
		{"used_ms=9&user_id=BBBBBB", []string{"B1"}},
		{"used_ms=1&user_id=AAAAAA&user_id=CCCCCC", []string{"B1"}},
	} {
		if codes, _ := search(c.query); !reflect.DeepEqual(c.want, codes) {
			t.Errorf("%s: %v, want %v", c.query, codes, c.want)
		}
	}
}
