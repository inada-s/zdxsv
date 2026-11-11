package main

import (
	"maps"
	"math"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"zdxsv/zdxsv/proto"

	pb "github.com/golang/protobuf/proto"
	"go.uber.org/zap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// OnGetBattleInfo returns the P2PMatching for an emulator that runs
// the zproxy bridge itself (platform info "udp=1"), else nil.
func (a *Lbs) OnGetBattleInfo(p *LbsPeer) *proto.P2PMatching {
	b := p.Battle
	if b == nil {
		return nil
	}
	return p2pMatching(p, func(userID string) map[string]string { return b.PlatformInfo[userID] })
}

// p2pMatching looks other players' platform info up by info.
// candidates: the other players' udp_addr, udp_local, udp_addr6 (platform info from
// their own bridge, looked up by info), then their lobby TCP source IP (tcpSourceAddr);
// users[].ggpo_port: platform info "ggpo": the client runs the battle over GGPO at
// those ports and the candidates' IPs when every other player has one, else over the bridge;
// session_id, ping_test_duration: set with any ggpo_port (the battle's id in the clients'
// ping test packets, its length in ms); a player alone with a GGPO port gets a session and no ping test;
// relays: the lobby's relay server (relayServers), when every player supports it;
// users[].team: the entry side (1 / 2);
// users[].sync: platform info "sync", self included: the client cuts the battle when one
// differs from its own.
var syncFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)

func p2pMatching(p *LbsPeer, info func(userID string) map[string]string) *proto.P2PMatching {
	b := p.Battle
	if b == nil || b.TestBattle || p.Platform == PlatformConsole || p.PlatformInfo["udp"] != "1" {
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
		if u.Entry == EntryAeug || u.Entry == EntryTitans {
			pu.Team = pb.Int32(int32(u.Entry))
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
	alone := len(users) == 1
	if port, err := strconv.Atoi(p.PlatformInfo["ggpo"]); alone && err == nil && 0 < port && port < 65536 {
		ggpo = true
	}
	if ggpo {
		// The session id is random, not derived from the battle code: the code is
		// public (live list, replays), and the session authorizes the battle's live upload.
		m.SessionId = pb.Uint32(b.GgpoSession)
		if !alone {
			m.PingTestDuration = pb.Int32(7500)
		}
		Spectators.Open(b.BattleCode, b.GgpoSession, liveUsers(b), time.Now())
		if liveUplink(p, users, info) == p.UserID {
			m.SpectatorUplink = pb.Bool(true)
		}
		m.Relays = relayServers(p, users, info, b.GgpoSession)
	}
	return m
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
func liveUplink(p *LbsPeer, users []string, info func(userID string) map[string]string) string {
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
func relayServers(p *LbsPeer, users []string, info func(userID string) map[string]string, session uint32) []*proto.RelayServer {
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

// OnP2PMatchingReport logs how a battle ran and saves its used_ms.
func (a *Lbs) OnP2PMatchingReport(p *LbsPeer, report *proto.P2PMatchingReport) {
	if p.PlatformInfo == nil {
		logger.Warn("p2p matching report without platform info ignored", zap.String("user_id", p.UserID))
		return
	}
	logger.Info("p2p matching report", p2pMatchingReportFields(report)...)
	sender := p.UserID
	if sender == "" { // the report comes before the user login
		sender = report.GetUserId()
	}
	saveUsedMs(report, sender)
}

// saveUsedMs sets each player's used_ms from a battle member's report round_data. Every
// member reports the same rounds; the last report wins.
func saveUsedMs(report *proto.P2PMatchingReport, sender string) {
	rounds := report.GetRoundData()
	if len(rounds) == 0 || report.GetBattleCode() == "" {
		return
	}
	records, err := DefaultDB.GetBattleRecords(report.GetBattleCode())
	if err != nil {
		logger.Warn("p2p matching report: GetBattleRecords failure", zap.Error(err))
		return
	}
	if !slices.ContainsFunc(records, func(r *BattleRecord) bool { return r.UserID == sender }) {
		return
	}
	for _, r := range records {
		var mask uint64
		var list []string
		for _, rd := range rounds {
			ms := 0
			if i := r.Pos - 1; 0 <= i && i < len(rd.GetUsedMs()) {
				ms = int(rd.GetUsedMs()[i])
			}
			if ms < 0 || 64 <= ms {
				ms = 0
			}
			list = append(list, strconv.Itoa(ms))
			if ms > 0 {
				mask |= 1 << ms
			}
		}
		if err := DefaultDB.SetUsedMs(r.BattleCode, r.UserID, mask, strings.Join(list, ",")); err != nil {
			logger.Warn("p2p matching report: SetUsedMs failure", zap.Error(err))
		}
	}
}

func p2pMatchingReportFields(report *proto.P2PMatchingReport) []zap.Field {
	m := report.ProtoReflect()
	fields := m.Descriptor().Fields()
	head := []protoreflect.Name{"battle_code", "user_id", "result"}
	var line []zap.Field
	for _, name := range head {
		line = append(line, zap.String(string(name), m.Get(fields.ByName(name)).String()))
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
		line = append(line, zap.String(string(fd.Name()), s))
	}
	return line
}

func battlePlatformInfo(peer *LbsPeer) map[string]string {
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

// liveUsers returns the battle's players for the /lbs/live list, as /lbs/replay's users.
func liveUsers(b *LbsBattle) []*ReplayUser {
	users := make([]*ReplayUser, 0, len(b.Users))
	for _, u := range b.Users {
		users = append(users, &ReplayUser{
			UserID:    u.UserID,
			UserName:  battleInfoName(u.Name),
			PilotName: pilotName(u.Bin),
			Team:      int(u.Entry),
			Pos:       int(b.GetPosition(u.UserID)),
		})
	}
	return users
}
