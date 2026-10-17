package model

import (
	"encoding/binary"
	"net"
	"reflect"
	"testing"

	"zdxsv/pkg/db"
)

func newUser(id string) *User {
	return &User{User: db.User{UserID: id}}
}

func TestRuleDefaults(t *testing.T) {
	r := NewRule()
	if RuleCount() != 10 {
		t.Fatalf("RuleCount = %d, want 10", RuleCount())
	}
	for id := byte(0); id < RuleCount(); id++ {
		if r.Get(id) != RuleElementDefault(id) {
			t.Errorf("rule %d: Get = %d, default %d", id, r.Get(id), RuleElementDefault(id))
		}
		if RuleElementDefault(id) >= RuleElementCount(id) {
			t.Errorf("rule %d: default %d out of %d elements", id, RuleElementDefault(id), RuleElementCount(id))
		}
		if RuleTitle(id) == "" || RuleElementName(id, 0) == "" {
			t.Errorf("rule %d: empty title or element name", id)
		}
	}
	if r.playerCount != 4 || r.battleTime != 210 || r.battleGauge != 600 || r.damageLevel != 3 {
		t.Errorf("default rule = %+v", r)
	}
}

func TestRuleSetGet(t *testing.T) {
	r := NewRule()
	r.Set(rulePlayerCount, 0)
	r.Set(ruleBattleCount, 4)
	r.Set(ruleVisibleTeam, 2)
	r.Set(ruleBattleTime, 0)
	r.Set(ruleBattleGauge, 6)
	r.Set(ruleReloadType, 1)
	r.Set(ruleDamageLevel, 0)
	r.Set(ruleAwakeType, 4)
	r.Set(ruleAwakeIncRatio, 0)
	r.Set(ruleAwakeDecRatio, 4)
	want := Rule{playerCount: 2, battleCount: 10, visibleTeam: 2, battleTime: 90, battleGauge: 1200,
		reloadType: 1, damageLevel: 1, awakeType: 4, awakeIncRatio: 10, awakeDecRatio: 200}
	got := *r
	got.selected = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rule = %+v, want %+v", got, want)
	}
	if r.Get(ruleBattleGauge) != 6 || r.Get(ruleAwakeDecRatio) != 4 {
		t.Errorf("Get does not return the selected element")
	}
	if NewRule().Get(ruleBattleGauge) != RuleElementDefault(ruleBattleGauge) {
		t.Errorf("Set leaked into a new rule")
	}
}

func TestRuleSerialize(t *testing.T) {
	r := NewRule()
	r.Set(ruleBattleGauge, 5) // 900
	r.Set(ruleDamageLevel, 3) // 4, sent as 3
	r.Set(ruleBattleCount, 2) // 3
	r.Set(ruleBattleTime, 4)  // 270
	b := r.Serialize()
	if len(b) != 102 || binary.BigEndian.Uint16(b) != 100 {
		t.Fatalf("len = %d, length field = %d; want 102, 100", len(b), binary.BigEndian.Uint16(b))
	}
	for i := 6; i < 70; i++ {
		if b[i] != 0xFF {
			t.Fatalf("MS mask byte %d = %x", i, b[i])
		}
	}
	u16 := func(off int) uint16 { return binary.BigEndian.Uint16(b[off:]) }
	if u16(72) != 900 || u16(74) != 900 || u16(76) != 100 || u16(78) != 100 || u16(80) != 270 {
		t.Errorf("gauge/awake/time = %d %d %d %d %d", u16(72), u16(74), u16(76), u16(78), u16(80))
	}
	if b[86] != 3 || b[87] != 3 || u16(100) != 1 {
		t.Errorf("damage %d, battle count %d, zz pilot %d", b[86], b[87], u16(100))
	}
}

func TestRoomLifecycle(t *testing.T) {
	r := NewRoom(2, 1)
	r.Rule.Set(rulePlayerCount, 0) // 2 players
	a, b := newUser("A"), newUser("B")

	r.Enter(a)
	if r.Owner != "A" || r.MaxPlayer != 2 || r.Status != RoomStateRecruit || r.Deadline.IsZero() {
		t.Fatalf("after first enter: %+v", r)
	}
	r.Enter(b)
	if r.Status != RoomStateFull {
		t.Errorf("status = %d, want full", r.Status)
	}
	r.Exit("nobody")
	if len(r.Users) != 2 || r.Status != RoomStateFull {
		t.Errorf("unknown user exit changed the room")
	}
	r.Exit("A")
	if len(r.Users) != 1 || r.Users[0] != b || r.Status != RoomStateRecruit {
		t.Errorf("after exit A: users %v, status %d", r.Users, r.Status)
	}
	r.Exit("B")
	if r.Status != RoomStateEmpty || r.Owner != "" || r.Name != "(空き)" || r.ID != 1 || r.LobbyID != 2 {
		t.Errorf("empty room not reset: %+v", r)
	}
}

func TestRoomBattleStart(t *testing.T) {
	r := NewRoom(2, 1)
	sides := map[string]byte{"a1": EntryAeug, "t1": EntryTitans, "a2": EntryAeug, "n": EntryNone, "a3": EntryAeug}
	order := []string{"a1", "t1", "a2", "n", "a3"}
	for _, id := range order {
		u := newUser(id)
		r.Enter(u)
		r.Entry(u, sides[id])
	}
	if a, b := r.GetEntryUserCount(); a != 3 || b != 1 {
		t.Fatalf("entry count = %d, %d", a, b)
	}
	if r.CanBattleStart() {
		t.Errorf("3 vs 1 can start")
	}
	active, inactive := r.StartBattleUsers()
	ids := func(us []*User) (s []string) {
		for _, u := range us {
			s = append(s, u.UserID)
		}
		return
	}
	if got := ids(active); len(got) != 3 || got[0] != "a1" || got[1] != "t1" || got[2] != "a2" {
		t.Errorf("active = %v", got)
	}
	if got := ids(inactive); len(got) != 2 || got[0] != "n" || got[1] != "a3" {
		t.Errorf("inactive = %v", got)
	}
	r.Exit("a3")
	if !r.CanBattleStart() {
		t.Errorf("2 vs 1 cannot start")
	}
	r.Entry(r.Users[1], EntryNone)
	if r.CanBattleStart() {
		t.Errorf("2 vs 0 can start")
	}
}

func TestLobbyEntry(t *testing.T) {
	l := NewLobby("ps2", 4)
	if l.RoomCount() != 5 || len(l.Rooms) != 6 || l.Rooms[5].LobbyID != 4 {
		t.Fatalf("rooms: count %d, map %d", l.RoomCount(), len(l.Rooms))
	}
	users := map[string]*User{}
	for _, id := range []string{"a1", "a2", "a3", "t1", "t2", "x"} {
		users[id] = newUser(id)
		l.Enter(users[id])
	}
	for _, id := range []string{"a1", "t1", "a2", "a3", "t2"} {
		side := byte(EntryAeug)
		if id[0] == 't' {
			side = EntryTitans
		}
		l.Entry(users[id], side)
	}
	if a, b := l.GetEntryUserCount(); a != 3 || b != 2 || !l.CanBattleStart() {
		t.Fatalf("count %d, %d, can start %v", a, b, l.CanBattleStart())
	}
	var got []string
	for _, u := range l.StartBattleUsers() {
		got = append(got, u.UserID)
	}
	if len(got) != 4 || got[0] != "a1" || got[1] != "t1" || got[2] != "a2" || got[3] != "t2" {
		t.Errorf("StartBattleUsers = %v", got)
	}

	l.Entry(users["t2"], EntryNone)
	if users["t2"].Entry != EntryNone || len(l.EntryUsers) != 4 || l.CanBattleStart() {
		t.Errorf("cancel entry: %v", l.EntryUsers)
	}
	l.Exit("a1")
	l.Exit("nobody")
	if _, ok := l.Users["a1"]; ok || len(l.EntryUsers) != 3 || len(l.Users) != 5 {
		t.Errorf("exit: users %d, entries %v", len(l.Users), l.EntryUsers)
	}
}

func TestLobbyCanBattleStartByID(t *testing.T) {
	cases := []struct {
		lobbyID uint16
		a, b    int
		want    bool
	}{
		{2, 1, 1, true}, {2, 1, 0, false},
		{3, 1, 0, true}, {3, 0, 1, true}, {3, 0, 0, false},
		{4, 1, 1, false}, {4, 2, 1, false}, {4, 2, 2, true},
	}
	for _, c := range cases {
		l := NewLobby("ps2", c.lobbyID)
		add := func(n int, side byte, p string) {
			for i := 0; i < n; i++ {
				u := newUser(p + string(rune('0'+i)))
				l.Enter(u)
				l.Entry(u, side)
			}
		}
		add(c.a, EntryAeug, "a")
		add(c.b, EntryTitans, "t")
		if got := l.CanBattleStart(); got != c.want {
			t.Errorf("lobby %d, %d vs %d: CanBattleStart = %v", c.lobbyID, c.a, c.b, got)
		}
	}
}

func TestBattle(t *testing.T) {
	b := NewBattle("ps2", 4)
	l := NewLobby("ps2", 4)
	a1, t1, n := newUser("a1"), newUser("t1"), newUser("n")
	a1.Entry, t1.Entry = EntryAeug, EntryTitans
	a1.Lobby = l
	for _, u := range []*User{a1, t1, n} {
		b.Add(u)
	}
	if b.Users[0].Lobby != nil || a1.Lobby != l {
		t.Errorf("Add must store a detached copy")
	}
	if b.NumOfEntryUsers() != 2 || len(b.AeugIDs) != 1 || len(b.TitansIDs) != 1 {
		t.Errorf("entries: %v %v", b.AeugIDs, b.TitansIDs)
	}
	if b.GetPosition("t1") != 2 || b.GetPosition("zz") != 0 {
		t.Errorf("GetPosition t1 = %d, zz = %d", b.GetPosition("t1"), b.GetPosition("zz"))
	}
	if u := b.GetUserByPos(1); u == nil || u.UserID != "a1" {
		t.Errorf("GetUserByPos(1) = %v", u)
	}
	b.SetBattleServer(net.IPv4(10, 0, 0, 1), 9000)
	r := NewRule()
	b.SetRule(r)
	if !b.ServerIP.Equal(net.IPv4(10, 0, 0, 1)) || b.ServerPort != 9000 || b.Rule != r {
		t.Errorf("server/rule not set")
	}
}
