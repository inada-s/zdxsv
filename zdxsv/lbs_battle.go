package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

type LbsBattle struct {
	BattleCode string
	ServerIP   net.IP
	ServerPort uint16
	Users      []LbsUser
	AeugIDs    []string
	TitansIDs  []string
	UDPUsers   map[string]bool
	P2PMap     map[string]map[string]struct{}
	Rule       *Rule
	LobbyID    uint16
	Platform   string
	StartTime  time.Time
	TestBattle bool
	// Ended: a player came back to the battle after room. The players end a battle together, so
	// the ones not back yet left it (a closed client) or are on their way; none is in it.
	Ended bool
	// Uplink: the session of the player that streams the battle live; its upload ends when the
	// player's client leaves the battle, and the session is then in Left.
	Uplink string
	Left   map[string]bool
	// GgpoSession: the GGPO and live uplink session id (battle info ggpo_session), random per
	// battle so that only its players know it (the battle code is public).
	GgpoSession uint32
	// PlatformInfo: each player's platform info when the battle was made, by user id.
	// The battle info reads it: a player leaves the lobby when it gets the battle
	// server address, before the others ask for theirs.
	PlatformInfo map[string]map[string]string
	// Patches: the lobby's game patches when the battle was made, sent to every emulator player.
	Patches []GamePatch
}

func NewBattle(platform string, lobbyID uint16) *LbsBattle {
	return &LbsBattle{
		Users:        make([]LbsUser, 0),
		AeugIDs:      make([]string, 0),
		TitansIDs:    make([]string, 0),
		UDPUsers:     map[string]bool{},
		P2PMap:       map[string]map[string]struct{}{},
		Rule:         NewRule(),
		LobbyID:      lobbyID,
		Platform:     platform,
		GgpoSession:  newGgpoSession(),
		PlatformInfo: map[string]map[string]string{},
	}
}

func (b *LbsBattle) SetRule(rule *Rule) {
	b.Rule = rule
}

func (b *LbsBattle) Add(s *LbsUser) {
	cp := *s
	cp.Battle = nil
	cp.Lobby = nil
	cp.Room = nil
	b.Users = append(b.Users, cp)
	if s.Entry == EntryAeug {
		b.AeugIDs = append(b.AeugIDs, cp.UserID)
	} else if s.Entry == EntryTitans {
		b.TitansIDs = append(b.TitansIDs, cp.UserID)
	}
}

func (b *LbsBattle) NumOfEntryUsers() uint16 {
	return uint16(len(b.AeugIDs) + len(b.TitansIDs))
}

func (b *LbsBattle) SetBattleServer(ip net.IP, port uint16) {
	b.ServerIP = ip
	b.ServerPort = port
}

func (b *LbsBattle) GetPosition(userID string) byte {
	for i, u := range b.Users {
		if userID == u.UserID {
			return byte(i + 1)
		}
	}
	return 0
}

func (b *LbsBattle) GetUserByPos(pos byte) *LbsUser {
	if pos == 0 || len(b.Users) < int(pos) {
		return nil
	}
	return &b.Users[pos-1]
}

type BattleResult struct {
	Unk01       uint16 `json:"unk_01"`
	BattleCode  string `json:"battle_code"`
	Unk03       byte   `json:"unk_03"`
	Unk04       byte   `json:"unk_04"`
	Unk05       byte   `json:"unk_05"`
	Unk06       byte   `json:"unk_06"`
	BattleCount byte   `json:"battle_count"`
	WinCount    byte   `json:"win_count"`
	LoseCount   byte   `json:"lose_count"`
	KillCount   uint32 `json:"kill_count"`
	DeathCount  uint32 `json:"death_count"`
	TotalFrame  uint32 `json:"total_frame"`
	Unk13       uint32 `json:"unk_13"`
	Unk14       uint32 `json:"unk_14"`
	Side        byte   `json:"side"`
	Unk16       byte   `json:"unk_16"`
	Unk17       byte   `json:"unk_17"`
	Unk18       byte   `json:"unk_18"`
	Unk19       byte   `json:"unk_19"`
	Unk20       uint16 `json:"unk_20"`
	Unk21       uint16 `json:"unk_21"`
	Unk22       uint16 `json:"unk_22"`
	Unk23       uint16 `json:"unk_23"`
	Unk24       uint16 `json:"unk_24"`
	Unk25       uint16 `json:"unk_25"`
	Unk26       uint16 `json:"unk_26"`
	Unk27       uint16 `json:"unk_27"`
}

// newGgpoSession returns a random nonzero session id.
func newGgpoSession() uint32 {
	var b [4]byte
	for {
		rand.Read(b[:])
		if s := binary.LittleEndian.Uint32(b[:]); s != 0 {
			return s
		}
	}
}

// P2PMatching is the battle info, lobby -> emulator custom notice 0x9951, as JSON.
type P2PMatching struct {
	BattleCode       string          `json:"battle_code"`
	SessionID        uint32          `json:"session_id,omitempty"`         // the battle's GGPO ping test session; 0: no GGPO
	PeerID           int             `json:"peer_id"`                      // the receiver's index in users
	PingTestDuration int             `json:"ping_test_duration,omitempty"` // ms
	Users            []P2PUser       `json:"users"`                        // every player, in battle order
	Candidates       []PlayerAddress `json:"candidates,omitempty"`         // the other players' UDP addresses
	Relays           []RelayServer   `json:"relays,omitempty"`
	SpectatorUplink  bool            `json:"spectator_uplink,omitempty"`
	LobbySessionID   string          `json:"lobby_session_id"`
	BattleServer     string          `json:"battle_server"` // "a.b.c.d:port"
}

type P2PUser struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name,omitempty"`
	PilotName string `json:"pilot_name,omitempty"`
	Team      int    `json:"team,omitempty"`      // 1 / 2 (entry side), 0: unknown
	GgpoPort  int    `json:"ggpo_port,omitempty"` // 0: none
	Sync      string `json:"sync,omitempty"`      // settings fingerprint, 16 lowercase hex digits
}

type PlayerAddress struct {
	UserID string `json:"user_id"`
	PeerID int    `json:"peer_id"`
	IP     string `json:"ip"` // IPv4 or IPv6, no brackets
	Port   int    `json:"port"`
}

type RelayServer struct {
	IP    string `json:"ip"`
	Port  int    `json:"port"`
	Token uint64 `json:"token"`
	IP6   string `json:"ip6,omitempty"`
	Port6 int    `json:"port6,omitempty"`
}

// OnGetBattleInfo returns the P2PMatching for an emulator in a lobby battle, else nil.
func (a *Lbs) OnGetBattleInfo(p *LbsPeer) *P2PMatching {
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
// users[].sync: platform info "sync", self included: pcsx2 zdxsv-1.1.2 and older cut the battle when one
// differs from their own or is missing; later releases send and read none.
var syncFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)

func p2pMatching(p *LbsPeer, info func(userID string) map[string]string) *P2PMatching {
	b := p.Battle
	if b == nil || b.TestBattle || p.Platform == PlatformConsole {
		return nil
	}
	if b.ServerIP == nil || b.ServerIP.To4() == nil || b.ServerPort == 0 {
		return nil
	}
	m := &P2PMatching{
		BattleCode:   b.BattleCode,
		BattleServer: net.JoinHostPort(b.ServerIP.String(), strconv.Itoa(int(b.ServerPort))),
	}
	var users []string
	self := false
	for i, u := range b.Users {
		users = append(users, u.UserID)
		pu := P2PUser{UserID: u.UserID}
		if u.UserID == p.UserID {
			m.LobbySessionID, self = u.SessionID, true
			m.PeerID = i
		}
		if name := battleInfoName(u.Name); name != "" {
			pu.UserName = name
		}
		if pilot := pilotName(u.Bin); pilot != "" {
			pu.PilotName = pilot
		}
		if u.Entry == EntryAeug || u.Entry == EntryTitans {
			pu.Team = int(u.Entry)
		}
		pi := p.PlatformInfo
		if u.UserID != p.UserID {
			pi = nil
			if info != nil {
				pi = info(u.UserID)
			}
		}
		if s := pi["sync"]; syncFingerprint.MatchString(s) {
			pu.Sync = s
		}
		m.Users = append(m.Users, pu)
	}
	if !self {
		return nil
	}
	ggpo := false
	for i, id := range users {
		if id == p.UserID || info == nil {
			continue
		}
		pi := info(id)
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
			m.Candidates = append(m.Candidates, PlayerAddress{
				UserID: id, PeerID: i, IP: ip, Port: port})
		}
		if port := ggpoPort(pi); port > 0 {
			m.Users[i].GgpoPort = port
			ggpo = true
		}
	}
	alone := len(users) == 1
	if alone && ggpoPort(p.PlatformInfo) > 0 {
		ggpo = true
	}
	if ggpo {
		// The session id is random, not derived from the battle code: the code is
		// public (live list, replays), and the session authorizes the battle's live upload.
		m.SessionID = b.GgpoSession
		if !alone {
			m.PingTestDuration = 7500
		}
		Spectators.Open(b.BattleCode, b.GgpoSession, liveUsers(b), time.Now())
		if liveUplink(p, users, info) == p.UserID {
			m.SpectatorUplink = true
			b.Uplink = p.SessionID
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
		if ggpoPort(pi) == 0 {
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
// when every player has GGPO (platform info ggpo). The relay session is the
// battle's ggpo_session, so every player gets the same token.
func relayServers(p *LbsPeer, users []string, info func(userID string) map[string]string, session uint32) []RelayServer {
	if info == nil || ggpoPort(p.PlatformInfo) == 0 {
		return nil
	}
	for _, id := range users {
		if id != p.UserID && ggpoPort(info(id)) == 0 {
			return nil
		}
	}
	playerInfo := func(id string) map[string]string {
		if id == p.UserID {
			return p.PlatformInfo
		}
		return info(id)
	}
	var relays []RelayServer
	for _, rr := range battleRelays(users, playerInfo) {
		ip, port, ok := splitUDPAddr(rr.PublicAddr)
		if !ok {
			continue
		}
		r := RelayServer{IP: ip, Port: port, Token: rr.Sessions.SessionToken(session)}
		if ip6, port6, ok := splitUDPAddr(rr.PublicAddr6); ok {
			r.IP6, r.Port6 = ip6, port6
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

// InBattle reports whether the player of sessionID is still in b.
func (b *LbsBattle) InBattle(sessionID string) bool {
	return !b.Ended && !b.Left[sessionID]
}

// LiveUploadEnded takes the live uplink of the battle out of it.
func (a *Lbs) LiveUploadEnded(battleCode string) {
	for _, b := range a.battles {
		if b.BattleCode == battleCode && b.Uplink != "" {
			if b.Left == nil {
				b.Left = map[string]bool{}
			}
			b.Left[b.Uplink] = true
		}
	}
}

// OnP2PMatchingReport logs a report body as received; the server reads nothing from it.
func (a *Lbs) OnP2PMatchingReport(p *LbsPeer, body []byte) {
	if p.PlatformInfo == nil {
		floodLog.Warn("p2p matching report without platform info ignored", zap.String("user_id", p.UserID))
		return
	}
	floodLog.Info("p2p matching report", zap.String("user_id", p.UserID), p2pMatchingReportField(body))
}

func p2pMatchingReportField(body []byte) zap.Field {
	if json.Valid(body) {
		return zap.Reflect("report", json.RawMessage(body))
	}
	return zap.ByteString("report", body)
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

// GamePatches is the lobby -> emulator custom notice 0x9953, as JSON: the game patches the
// emulator applies during the battle. Sent before the battle info.
type GamePatches struct {
	Patches []GamePatch `json:"patches"`
}

type GamePatch struct {
	Name      string          `json:"name"`
	Game      string          `json:"game"`
	WriteOnce bool            `json:"write_once,omitempty"` // false: written every frame
	Codes     []GamePatchCode `json:"codes"`
}

type GamePatchCode struct {
	Size     int    `json:"size"` // bits: 8, 16, 32
	Address  uint32 `json:"address"`
	Original uint32 `json:"original"`
	Changed  uint32 `json:"changed"`
}

// ParsePatchCodes reads MPatch.Codes: address in hex ("0x" optional), values in decimal or
// "0x" hex. Blank lines are skipped.
func ParsePatchCodes(codes string) ([]GamePatchCode, error) {
	var out []GamePatchCode
	for n, line := range strings.Split(codes, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: %q is not size,address,original,changed", n+1, line)
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		size, err := strconv.Atoi(f[0])
		if err != nil || (size != 8 && size != 16 && size != 32) {
			return nil, fmt.Errorf("line %d: size %q is not 8, 16 or 32", n+1, f[0])
		}
		addr, err := strconv.ParseUint(strings.TrimPrefix(f[1], "0x"), 16, 32)
		if err != nil || addr%uint64(size/8) != 0 {
			return nil, fmt.Errorf("line %d: address %q is not a hex address aligned to the size", n+1, f[1])
		}
		c := GamePatchCode{Size: size, Address: uint32(addr)}
		for i, v := range []*uint32{&c.Original, &c.Changed} {
			x, err := strconv.ParseUint(f[2+i], 0, size)
			if err != nil {
				return nil, fmt.Errorf("line %d: value %q does not fit %d bits", n+1, f[2+i], size)
			}
			*v = uint32(x)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no code")
	}
	return out, nil
}

// lobbyPatches is the GamePatch list of a lobby setting's patches; a missing or bad one is skipped.
func lobbyPatches(s *MLobbySetting) []GamePatch {
	var patches []GamePatch
	for _, name := range strings.Split(s.Patches, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		p, err := DefaultDB.GetPatch(s.Platform, name)
		if err != nil || p == nil {
			logger.Warn("m_lobby_setting patch", zap.Any("platform", s.Platform), zap.Any("no", s.No), zap.Any("patch", name), zap.Error(err))
			continue
		}
		codes, err := ParsePatchCodes(p.Codes)
		if err != nil {
			logger.Warn("m_patch codes", zap.Any("platform", p.Platform), zap.Any("name", p.Name), zap.Error(err))
			continue
		}
		patches = append(patches, GamePatch{Name: p.Name, Game: p.Game, WriteOnce: p.WriteOnce, Codes: codes})
	}
	return patches
}

// OnGetGamePatches returns the battle's patches for an emulator player, nil for none.
func (a *Lbs) OnGetGamePatches(p *LbsPeer) *GamePatches {
	b := p.Battle
	if b == nil || p.Platform != PlatformEmu || len(b.Patches) == 0 {
		return nil
	}
	return &GamePatches{Patches: b.Patches}
}

func ggpoPort(pi map[string]string) int {
	port, err := strconv.Atoi(pi["ggpo"])
	if err != nil || port <= 0 || 65536 <= port {
		return 0
	}
	return port
}
