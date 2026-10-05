package lobby

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	. "zdxsv/pkg/lobby/message"

	"zdxsv/pkg/lobby/model"

	"encoding/json"

	"github.com/golang/glog"
)

func NoticeBattleStart(p *AppPeer) {
	n := NewServerNotice(0x6910)
	p.SendMessage(n)
}

var _ = register(0x6911, "GetBattleUserCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	count := p.app.OnGetBattleUserCount(p)
	w := a.Writer()
	if count > 0 {
		w.Write8(count) // ユーザ数
	} else {
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6912, "GetBattleUserPosition", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	pos := p.app.OnGetBattleUserPosition(p)
	if pos > 0 {
		w.Write8(pos)
	} else {
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6913, "GetBattleOpponentUser", func(p *AppPeer, m *Message) {
	pos := m.Reader().Read8()
	user := p.app.OnGetBattleOpponentUser(p, pos)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(pos)
	if user != nil {
		w.Write8(user.Entry)
		w.WriteString(user.UserID)
		w.WriteString(user.Name)
		w.WriteString(user.Team)
		w.WriteString(user.Bin)
		w.Write8(pos) // 不明
	} else {
		glog.Infoln("UserPos not found", pos)
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6917, "GetBattleOpponentStatus", func(p *AppPeer, m *Message) {
	pos := m.Reader().Read8()
	a := NewServerAnswer(m)
	w := a.Writer()
	user := p.app.OnGetBattleOpponentUser(p, pos)
	w.Write8(pos)

	// TODO: Consider a reasonable calculation method.
	// class 14 ~ 0 : [大将][中将][少将][大佐][中佐][少佐][大尉][中尉][少尉][曹長][軍曹][伍長][上等兵][一等兵][二等兵]
	c := uint16(user.WinCount / 100)
	if 14 <= c {
		c = 14
	}
	w.Write16(c)
	w.Write32(0) // Unknown
	w.Write32(uint32(user.BattleCount))
	w.Write32(uint32(user.WinCount))
	w.Write32(uint32(user.LoseCount))
	w.Write32(0) // Unknown

	p.SendMessage(a)
})

var _ = register(0x6914, "GetBattleRule", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	rule := p.app.OnGetBattleRule(p)
	w.Write(rule.Serialize())
	p.SendMessage(a)
})

var _ = register(0x6915, "GetBattleBattleCode", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	battleCode, err := p.app.OnGetBattleCode(p)
	if err == nil {
		w := a.Writer()
		w.WriteString(battleCode)
	} else {
		glog.Infoln("Failed to get battle code")
		a.Status = StatusError
	}
	p.SendMessage(a)
})

// battleInfoNotice returns the custom notice 0x9951 for an emulator that runs
// the zproxy bridge itself (platform info "udp=1"), else nil.
// Sent just before the battle server address: the emulator strips it from the
// game's stream and bridges the game's battle TCP to the battle server over UDP.
// Body: "key=value" lines like 0x9950; users = every player's user id, in battle order;
// p2p_<user id> = that player's udp_addr,udp_local,udp_addr6 (IPv4 public, local, "[IPv6]:port";
// platform info from its own
// bridge, looked up by info) for direct peering, only for players that sent them;
// ggpo_<user id> = that player's GGPO UDP port (platform info "ggpo"), only for
// bridges that sent one: the client runs the battle over GGPO at those ports and
// the p2p_ addresses' IPs when every other player has one, else over the bridge;
// battle_code = the battle's code (echoed in the client's P2PMatchingReport);
// with any ggpo_ line: ggpo_session = the battle's id in the clients' ping test
// packets (flycast UdpPingPong), ggpo_ping_ms = its length (input delay from rtt);
// relay_0 = the lobby's relay server (relayLine), when every player supports it.
func battleInfoNotice(p *AppPeer, info func(userID string) map[string]string) *Message {
	b := p.Battle
	if b == nil || b.TestBattle || p.Platform == model.PlatformConsole || p.PlatformInfo["udp"] != "1" {
		return nil
	}
	if b.ServerIP == nil || b.ServerIP.To4() == nil || b.ServerPort == 0 {
		return nil
	}
	sessionID := ""
	var users []string
	for _, u := range b.Users {
		users = append(users, u.UserID)
		if u.UserID == p.UserID {
			sessionID = u.SessionID
		}
	}
	if sessionID == "" {
		return nil
	}
	p2p := ""
	ggpo := false
	for _, id := range users {
		if id == p.UserID || info == nil {
			continue
		}
		pi := info(id)
		if pi["udp"] != "1" {
			continue
		}
		var addrs []string
		for _, k := range []string{"udp_addr", "udp_local", "udp_addr6"} {
			if a := pi[k]; a != "" && !strings.ContainsAny(a, ",\n") {
				addrs = append(addrs, a)
			}
		}
		if len(addrs) > 0 {
			p2p += "p2p_" + id + "=" + strings.Join(addrs, ",") + "\n"
		}
		if port, err := strconv.Atoi(pi["ggpo"]); err == nil && 0 < port && port < 65536 {
			p2p += "ggpo_" + id + "=" + strconv.Itoa(port) + "\n"
			ggpo = true
		}
	}
	if ggpo {
		// as gdxsv's P2PMatching: session id = fnv32 of the battle code, ping test 7500 ms
		h := fnv.New32()
		h.Write([]byte(b.BattleCode))
		p2p += "ggpo_session=" + strconv.FormatUint(uint64(h.Sum32()), 10) + "\nggpo_ping_ms=7500\n"
		if line := relayLine(p, users, info, h.Sum32()); line != "" {
			p2p += line
		}
	}
	n := NewServerNotice(0x9951)
	n.Category = CategoryCustom
	n.Body = []byte("session_id=" + sessionID + "\n" +
		"user_id=" + p.UserID + "\n" +
		"battle_code=" + b.BattleCode + "\n" +
		"battle_server=" + b.ServerIP.String() + ":" + strconv.Itoa(int(b.ServerPort)) + "\n" +
		"users=" + strings.Join(users, ",") + "\n" +
		p2p)
	return n
}

// relayLine offers the lobby's relay to a GGPO battle as gdxsv's P2PMatching.relays, only
// when every player reports relay_server=1 (platform info): "relay_0=<token hex>,<ip:port>
// [,<[ip6]:port>]". The relay session is the battle's ggpo_session, so every player gets the
// same token.
func relayLine(p *AppPeer, users []string, info func(userID string) map[string]string, session uint32) string {
	if LobbyRelay == nil || RelayPublicAddr == "" || info == nil || p.PlatformInfo["relay_server"] != "1" {
		return ""
	}
	for _, id := range users {
		if id != p.UserID && info(id)["relay_server"] != "1" {
			return ""
		}
	}
	line := "relay_0=" + strconv.FormatUint(LobbyRelay.SessionToken(session), 16) + "," + RelayPublicAddr
	if RelayPublicAddr6 != "" {
		line += "," + RelayPublicAddr6
	}
	return line + "\n"
}

// P2PMatchingReport is sent by emulators with lobby GGPO (pcsx2 ZDXSV_GGPO lobby=1), as
// gdxsv's lbsP2PMatchingReport: on the first lobby connection after a battle, after the
// platform info, "key=value" lines on how the battle ran (battle_code, user_id, result =
// ggpo / cut / server, per-peer ping test rtt, delay, frames, close reason).
// Custom category, no answer: logged for server-side stats and troubleshooting.
var _ = register(0x9952, "P2PMatchingReport", func(p *AppPeer, m *Message) {
	if p.PlatformInfo == nil {
		glog.Warningln("p2p matching report without platform info ignored", p.UserID)
		return
	}
	glog.Infoln(p2pMatchingReportLine(model.ParsePlatformInfo(string(m.Body))))
})

// p2pMatchingReportLine formats a report as one log line: battle_code, user_id and result
// first, then the other keys sorted.
func p2pMatchingReportLine(report map[string]string) string {
	head := []string{"battle_code", "user_id", "result"}
	var rest []string
	for k := range report {
		if k != head[0] && k != head[1] && k != head[2] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	line := "p2p matching report:"
	for _, k := range append(head, rest...) {
		line += " " + k + "=" + strconv.Quote(report[k])
	}
	return line
}

var _ = register(0x6916, "GetBattleServerAddress", func(p *AppPeer, m *Message) {
	ip, port := p.app.OnGetBattleServerAddress(p)
	info := func(userID string) map[string]string {
		if peer, ok := p.app.users[userID]; ok {
			return peer.PlatformInfo
		}
		return nil
	}
	if n := battleInfoNotice(p, info); n != nil {
		glog.Infoln("udp bridge battle info", p.UserID)
		p.SendMessage(n)
	}
	a := NewServerAnswer(m)

	if ip == nil || ip.To4() == nil || port == 0 {
		a.Status = StatusError
	} else {
		bits := strings.Split(ip.String(), ".")
		b0, _ := strconv.Atoi(bits[0])
		b1, _ := strconv.Atoi(bits[1])
		b2, _ := strconv.Atoi(bits[2])
		b3, _ := strconv.Atoi(bits[3])
		w := a.Writer()
		w.Write16(4)
		w.Write8(byte(b0))
		w.Write8(byte(b1))
		w.Write8(byte(b2))
		w.Write8(byte(b3))
		w.Write16(2)
		w.Write16(port)
	}
	p.SendMessage(a)
})

var _ = register(0x6210, "EnterBattleAfterRoom", func(p *AppPeer, m *Message) {
	p.app.OnEnterBattleAfterRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6211, "ExitBattleAfterRoom", func(p *AppPeer, m *Message) {
	p.app.OnExitBattleAfterRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6212, "GetBattleAfterRoomUserCount", func(p *AppPeer, m *Message) {
	a := NewServerAnswer(m)
	w := a.Writer()
	n := p.app.OnGetBattleAfterRoomUserCount(p)
	w.Write16(n)
	p.SendMessage(a)
})

func NoticeBattleAfterRoomUserCount(p *AppPeer, count uint16) {
	n := NewServerNotice(0x6212)
	w := n.Writer()
	w.Write16(count)
	p.SendMessage(n)
}

func AskBattleResult(p *AppPeer) {
	p.SendMessage(NewServerQuestion(0x6138))
}

func parseBattleResult(m *Message) *model.BattleResult {
	r := m.Reader()
	v01 := r.Read16()
	v02 := r.ReadEncryptedString()
	v03 := r.Read8()
	v04 := r.Read8()
	v05 := r.Read8()
	v06 := r.Read8()
	v07 := r.Read8()
	v08 := r.Read8()
	v09 := r.Read8()
	v10 := r.Read32()
	v11 := r.Read32()
	v12 := r.Read32()
	v13 := r.Read32()
	v14 := r.Read32()
	v15 := r.Read8()
	v16 := r.Read8()
	v17 := r.Read8()
	v18 := r.Read8()
	v19 := r.Read8()
	v20 := r.Read16()
	v21 := r.Read16()
	v22 := r.Read16()
	v23 := r.Read16()
	v24 := r.Read16()
	v25 := r.Read16()
	v26 := r.Read16()
	v27 := r.Read16()
	return &model.BattleResult{
		v01, v02, v03, v04, v05, v06, v07, v08,
		v09, v10, v11, v12, v13, v14, v15, v16,
		v17, v18, v19, v20, v21, v22, v23, v24,
		v25, v26, v27,
	}
}

var _ = register(0x6138, "AnswerBattleResult", func(p *AppPeer, m *Message) {
	glog.Infoln("== BattleResult ==")
	glog.Infoln("ID:", p.User.UserID)
	glog.Infoln("Name:", p.User.Name)
	result := parseBattleResult(m)
	js, _ := json.MarshalIndent(result, "", "  ")
	glog.Infoln(string(js))
	glog.Infoln("==================")

	p.app.OnGetBattleResult(p, result)
})
