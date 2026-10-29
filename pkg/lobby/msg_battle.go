package lobby

import (
	"maps"
	"math"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	. "zdxsv/pkg/lobby/message"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"
	"zdxsv/pkg/proto"

	"encoding/json"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/reflect/protoreflect"
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
		w.Write8(count)
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
	if user == nil {
		glog.Infoln("UserPos not found", pos)
		a.Status = StatusError
		p.SendMessage(a)
		return
	}

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
	if rule == nil {
		a.Status = StatusError
	} else {
		w.Write(rule.Serialize())
	}
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
// Sent just before the battle server address. Body: a P2PMatching (zdxsv.proto).
// candidates: the other players' udp_addr, udp_local, udp_addr6 (platform info from
// their own bridge, looked up by info), then their lobby TCP source IP (tcpSourceAddr);
// users[].ggpo_port: platform info "ggpo": the client runs the battle over GGPO at
// those ports and the candidates' IPs when every other player has one, else over the bridge;
// session_id, ping_test_duration: set with any ggpo_port (the battle's id in the clients'
// ping test packets, its length in ms);
// relays: the lobby's relay server (relayServers), when every player supports it;
// users[].sync: platform info "sync", self included: the client cuts the battle when one
// differs from its own.
var syncFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)

func battleInfoNotice(p *AppPeer, info func(userID string) map[string]string) *Message {
	b := p.Battle
	if b == nil || b.TestBattle || p.Platform == model.PlatformConsole || p.PlatformInfo["udp"] != "1" {
		return nil
	}
	if b.ServerIP == nil || b.ServerIP.To4() == nil || b.ServerPort == 0 {
		return nil
	}
	m := &proto.P2PMatching{
		BattleCode:   pb.String(b.BattleCode),
		BattleServer: pb.String(net.JoinHostPort(b.ServerIP.String(), strconv.Itoa(int(b.ServerPort)))),
	}
	var users []string
	for i, u := range b.Users {
		users = append(users, u.UserID)
		pu := &proto.P2PUser{UserId: pb.String(u.UserID)}
		if u.UserID == p.UserID {
			m.LobbySessionId = pb.String(u.SessionID)
			m.PeerId = pb.Int32(int32(i))
		}
		if name := battleInfoName(u.Name); name != "" {
			pu.UserName = pb.String(name)
		}
		if pilot := pilotName(u.Bin); pilot != "" {
			pu.PilotName = pb.String(pilot)
		}
		pi := p.PlatformInfo
		if u.UserID != p.UserID {
			pi = nil
			if info != nil {
				pi = info(u.UserID)
			}
		}
		if s := pi["sync"]; syncFingerprint.MatchString(s) {
			pu.Sync = pb.String(s)
		}
		m.Users = append(m.Users, pu)
	}
	if m.LobbySessionId == nil {
		return nil
	}
	ggpo := false
	for i, id := range users {
		if id == p.UserID || info == nil {
			continue
		}
		pi := info(id)
		if pi["udp"] != "1" {
			continue
		}
		var addrs []string
		for _, k := range []string{"udp_addr", "udp_local", "udp_addr6"} {
			if _, _, ok := splitUDPAddr(pi[k]); ok {
				addrs = append(addrs, pi[k])
			}
		}
		if a := tcpSourceAddr(pi["tcp_ip"], addrs); a != "" {
			addrs = append(addrs, a)
		}
		for _, a := range addrs {
			ip, port, _ := splitUDPAddr(a)
			m.Candidates = append(m.Candidates, &proto.PlayerAddress{
				UserId: pb.String(id), PeerId: pb.Int32(int32(i)), Ip: pb.String(ip), Port: pb.Int32(int32(port))})
		}
		if port, err := strconv.Atoi(pi["ggpo"]); err == nil && 0 < port && port < 65536 {
			m.Users[i].GgpoPort = pb.Int32(int32(port))
			ggpo = true
		}
	}
	if ggpo {
		// The session id is random, not derived from the battle code: the code is
		// public (live list, replays), and the first push with the session becomes the battle's live uplink.
		m.SessionId = pb.Uint32(b.GgpoSession)
		m.PingTestDuration = pb.Int32(7500)
		Spectators.Open(b.BattleCode, b.GgpoSession, liveUsers(b), time.Now())
		if liveUplink(p, users, info) == p.UserID {
			m.SpectatorUplink = pb.Bool(true)
		}
		m.Relays = relayServers(p, users, info, b.GgpoSession)
	}
	body, err := pb.Marshal(m)
	if err != nil {
		glog.Errorln("battle info:", err)
		return nil
	}
	n := NewServerNotice(0x9951)
	n.Category = CategoryCustom
	n.Body = body
	return n
}

// splitUDPAddr splits "ip:port" or "[ip6]:port" with a numeric IP and a port in 1..65535.
func splitUDPAddr(a string) (string, int, bool) {
	h, ps, err := net.SplitHostPort(a)
	if err != nil || net.ParseIP(h) == nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port <= 0 || 65535 < port {
		return "", 0, false
	}
	return h, port, true
}

// tcpSourceAddr returns ip (the lobby TCP connection's source) with
// the port of the first of addrs, when no address in addrs has that IP; else "".
// Without a STUN answer a player sends only its local address: its public IP is then
// the TCP source, with the local port for NATs that keep it.
func tcpSourceAddr(ip string, addrs []string) string {
	if net.ParseIP(ip) == nil || len(addrs) == 0 {
		return ""
	}
	port := ""
	for _, a := range addrs {
		h, p, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		if net.ParseIP(h).Equal(net.ParseIP(ip)) {
			return ""
		}
		if port == "" {
			port = p
		}
	}
	if port == "" {
		return ""
	}
	return net.JoinHostPort(ip, port)
}

// liveUplink returns the GGPO player that streams the battle to live spectators
// (Spectators): the lowest udp_rtt (platform info, ms to the lobby's UDP
// socket), ties and unknown in battle order; "" if no player has GGPO.
func liveUplink(p *AppPeer, users []string, info func(userID string) map[string]string) string {
	best, bestRtt := "", math.MaxInt32
	for _, id := range users {
		pi := p.PlatformInfo
		if id != p.UserID {
			if info == nil {
				continue
			}
			pi = info(id)
		}
		if port, err := strconv.Atoi(pi["ggpo"]); err != nil || port <= 0 || 65536 <= port {
			continue
		}
		rtt, err := strconv.Atoi(pi["udp_rtt"])
		if err != nil || rtt < 0 {
			rtt = math.MaxInt32 - 1
		}
		if rtt < bestRtt {
			best, bestRtt = id, rtt
		}
	}
	return best
}

// relayServers offers the relays of battleRelays to a GGPO battle, only
// when every player reports relay_server=1 (platform info). The relay session is the
// battle's ggpo_session, so every player gets the same token.
func relayServers(p *AppPeer, users []string, info func(userID string) map[string]string, session uint32) []*proto.RelayServer {
	if info == nil || p.PlatformInfo["relay_server"] != "1" {
		return nil
	}
	for _, id := range users {
		if id != p.UserID && info(id)["relay_server"] != "1" {
			return nil
		}
	}
	playerInfo := func(id string) map[string]string {
		if id == p.UserID {
			return p.PlatformInfo
		}
		return info(id)
	}
	var relays []*proto.RelayServer
	for _, rr := range battleRelays(users, playerInfo) {
		ip, port, ok := splitUDPAddr(rr.PublicAddr)
		if !ok {
			continue
		}
		r := &proto.RelayServer{Ip: pb.String(ip), Port: pb.Int32(int32(port)), Token: pb.Uint64(rr.Sessions.SessionToken(session))}
		if ip6, port6, ok := splitUDPAddr(rr.PublicAddr6); ok {
			r.Ip6, r.Port6 = pb.String(ip6), pb.Int32(int32(port6))
		}
		relays = append(relays, r)
	}
	return relays
}

// battleInfoName returns a player's name without control characters; "" if none.
func battleInfoName(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
}

// userBinaryField is the size of each Shift-JIS field of the game's user binary
// (SetUserBinary, 110 bytes): pilot name, then four quick chat messages.
const userBinaryField = 22

// pilotName returns the pilot name in a user binary: the first field up to its NUL,
// without control characters; "" if none.
func pilotName(bin string) string {
	b, _, err := transform.String(japanese.ShiftJIS.NewEncoder(), bin)
	if err != nil {
		return ""
	}
	if len(b) > userBinaryField {
		b = b[:userBinaryField]
	}
	if i := strings.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	s, _, err := transform.String(japanese.ShiftJIS.NewDecoder(), b)
	if err != nil {
		return ""
	}
	return battleInfoName(s)
}

// P2PMatchingReport is sent by emulators with lobby GGPO on the first lobby connection
// after a battle, after the platform info: a P2PMatchingReport (zdxsv.proto) on how the
// battle ran. Custom category, no answer: logged for server-side stats and troubleshooting.
var _ = register(0x9952, "P2PMatchingReport", func(p *AppPeer, m *Message) {
	if p.PlatformInfo == nil {
		glog.Warningln("p2p matching report without platform info ignored", p.UserID)
		return
	}
	report := &proto.P2PMatchingReport{}
	if err := pb.Unmarshal(m.Body, report); err != nil {
		glog.Warningln("p2p matching report:", p.UserID, err)
		return
	}
	glog.Infoln(p2pMatchingReportLine(report))
	Spectators.End(report.GetBattleCode(), p.UserID, time.Now())
})

// p2pMatchingReportLine formats a report as one log line of name="value": battle_code,
// user_id and result first, then the set fields in field order.
func p2pMatchingReportLine(report *proto.P2PMatchingReport) string {
	m := report.ProtoReflect()
	fields := m.Descriptor().Fields()
	head := []protoreflect.Name{"battle_code", "user_id", "result"}
	line := "p2p matching report:"
	for _, name := range head {
		line += " " + string(name) + "=" + strconv.Quote(m.Get(fields.ByName(name)).String())
	}
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if slices.Contains(head, fd.Name()) || !m.Has(fd) {
			continue
		}
		s := m.Get(fd).String()
		if fd.IsList() {
			var items []string
			for j, l := 0, m.Get(fd).List(); j < l.Len(); j++ {
				if fd.Message() == nil {
					items = append(items, l.Get(j).String())
				} else {
					items = append(items, prototext.MarshalOptions{}.Format(l.Get(j).Message().Interface()))
				}
			}
			s = strings.Join(items, ", ")
		}
		line += " " + string(fd.Name()) + "=" + strconv.Quote(s)
	}
	return line
}

var _ = register(0x6916, "GetBattleServerAddress", func(p *AppPeer, m *Message) {
	ip, port := p.app.OnGetBattleServerAddress(p)
	info := func(userID string) map[string]string {
		peer, ok := p.app.users[userID]
		if !ok {
			return nil
		}
		pi := maps.Clone(peer.PlatformInfo)
		if pi == nil {
			pi = map[string]string{}
		}
		pi["tcp_ip"] = "" // the server's, never the client's
		if peer.conn != nil {
			if h, _, err := net.SplitHostPort(peer.conn.Address()); err == nil {
				pi["tcp_ip"] = h
			}
		}
		return pi
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
		Unk01: v01, BattleCode: v02, Unk03: v03, Unk04: v04,
		Unk05: v05, Unk06: v06, BattleCount: v07, WinCount: v08,
		LoseCount: v09, KillCount: v10, DeathCount: v11, TotalFrame: v12,
		Unk13: v13, Unk14: v14, Side: v15, Unk16: v16,
		Unk17: v17, Unk18: v18, Unk19: v19, Unk20: v20,
		Unk21: v21, Unk22: v22, Unk23: v23, Unk24: v24,
		Unk25: v25, Unk26: v26, Unk27: v27,
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

// liveUsers returns the battle's players for the /lbs/live list, as /lbs/replay's users.
func liveUsers(b *model.Battle) []*db.ReplayUser {
	users := make([]*db.ReplayUser, 0, len(b.Users))
	for _, u := range b.Users {
		users = append(users, &db.ReplayUser{
			UserID:    u.UserID,
			UserName:  battleInfoName(u.Name),
			PilotName: pilotName(u.Bin),
			Team:      int(u.Entry),
			Pos:       int(b.GetPosition(u.UserID)),
		})
	}
	return users
}
