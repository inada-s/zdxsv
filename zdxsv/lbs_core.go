package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	"go.uber.org/zap"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// ===========================
// Login
// ===========================

func (a *Lbs) OnOpen(p *LbsPeer) {
	RequestKeyPair(p)
}

func (a *Lbs) OnClose(p *LbsPeer) {
	if p.inBattleAfterRoom {
		a.OnExitBattleAfterRoom(p)
	}
	a.OnExitLobby(p)
	a.removeUser(p)
}

// closePeer logs p out at once and closes its connection after a shutdown notice.
// Serve drops p's messages still queued: lobby and room members are keyed by user id,
// so they would act on a later client of the same user.
func (a *Lbs) closePeer(p *LbsPeer) {
	a.OnClose(p)
	delete(a.peers, p.conn.Address())
	SendServerShutDown(p)
	p.conn.Close()
}

// removeUser forgets p as its user unless a peer that logged in later holds the user id.
func (a *Lbs) removeUser(p *LbsPeer) {
	if a.users[p.UserID] == p {
		delete(a.users, p.UserID)
	}
}

func (a *Lbs) OnKeePair(p *LbsPeer, loginKey, sessionID string) {
	accountKey, ok := Sessions.Use(sessionID, loginKey)
	if !ok {
		logger.Error("no login session", zap.Any("session_id", sessionID))
		return
	}
	ac, err := DefaultDB.GetAccountByLoginKey(accountKey)
	if err != nil {
		logger.Error("login failed", zap.Error(err))
		return
	}

	ip := p.IP()
	machineID := p.PlatformInfo["machine_id"]
	if len(machineID) > maxMachineIDLen {
		machineID = machineID[:maxMachineIDLen]
	}
	if err := DefaultDB.SetLoginEndpoint(ac.LoginKey, ip, machineID); err != nil {
		logger.Error("SetLoginEndpoint failure", zap.Error(err))
	}
	ban, err := DefaultDB.FindBan(ac.LoginKey)
	if err != nil {
		logger.Error("FindBan failure", zap.Error(err))
	}
	if ban != nil {
		logger.Info("banned login refused", zap.Any("login_key", ac.LoginKey), zap.Any("ip", ip), zap.Any("machine_id", machineID), zap.Any("ban_key", ban.Key), zap.Any("until", ban.Until))
		SendBanned(p)
		return
	}
	if pcsx2NeedsUpdate(p.PlatformInfo, ac.LoginKey, machineID) {
		logger.Info("outdated client refused", zap.Any("login_key", ac.LoginKey), zap.Any("machine_id", machineID), zap.String("pcsx2", p.PlatformInfo["pcsx2"]))
		SendPleaseUpdate(p)
		return
	}

	p.LoginKey = ac.LoginKey
	p.SessionID = sessionID
	RequestFirstData(p)
}

func (a *Lbs) OnFirstData(p *LbsPeer) {
	if p.LoginKey == "" {
		logger.Error("loginKey not set")
		return
	}
	battle, ok := a.battles[p.SessionID]
	if ok {
		for _, u := range battle.Users {
			if u.SessionID == p.SessionID {
				if u.LoginKey != p.LoginKey {
					logger.Error("user of another account refused", zap.String("user_id", u.UserID), zap.String("login_key", p.LoginKey))
					return
				}
				p.LbsUser = u
				break
			}
		}
		p.LbsUser.Entry = EntryNone
		NoticeUserIDList(p, []*DBUser{&p.LbsUser.DBUser})
	} else {
		users, err := DefaultDB.GetUserList(p.LoginKey)
		if err != nil {
			logger.Error("login failed", zap.Error(err))
			return
		}
		NoticeUserIDList(p, users)
	}
}

// OnDecideUserID logs p in as a user of its account; false when refused. The users of an
// account may play at once, from separate clients, but each user from one client only.
func (a *Lbs) OnDecideUserID(p *LbsPeer, userID, name string) bool {
	sessionID := p.SessionID

	if userID == "******" {
		u, err := DefaultDB.RegisterUser(p.LoginKey)
		if err != nil {
			logger.Error("login failed", zap.Error(err))
			return false
		}
		p.LbsUser.DBUser = *u
		p.Name = name
		p.SessionID = sessionID
	} else if _, ok := a.battles[p.SessionID]; ok {
		if p.UserID == "" {
			logger.Error("no battle user for session", zap.Any("session_id", p.SessionID))
			return false
		}
	} else if len(name) == 0 || userID == string([]byte{0, 0, 0, 0, 0, 0}) {
		// use last login user_id
		ac, err := DefaultDB.GetAccountByLoginKey(p.LoginKey)
		if err != nil {
			logger.Error("login failed", zap.Error(err))
			return false
		}
		u, err := DefaultDB.GetUser(ac.LastUserID)
		if err != nil {
			logger.Error("login failed", zap.Error(err))
			return false
		}
		if u.LoginKey != p.LoginKey {
			logger.Error("user of another account refused", zap.String("user_id", u.UserID), zap.String("login_key", p.LoginKey))
			return false
		}
		p.LbsUser.DBUser = *u
		p.SessionID = sessionID
	} else if 0 < len(userID) && 0 < len(name) {
		u, err := DefaultDB.GetUser(userID)
		if err != nil {
			logger.Error("login failed", zap.Error(err))
			return false
		}
		if u.LoginKey != p.LoginKey {
			logger.Error("user of another account refused", zap.String("user_id", u.UserID), zap.String("login_key", p.LoginKey))
			return false
		}
		p.LbsUser.DBUser = *u
		p.Name = name
		p.SessionID = sessionID
	} else {
		logger.Error("undefined user id", zap.Any("user_id", userID), zap.Any("name", name), zap.Any("session_id", sessionID))
		return false
	}

	if other, ok := a.users[p.UserID]; ok && other != p {
		logger.Info("user logged in again, closing its previous client", zap.String("user_id", p.UserID), zap.Any("addr", other.conn.Address()))
		a.closePeer(other)
	}

	err := DefaultDB.LoginUser(&p.LbsUser.DBUser)
	if err != nil {
		logger.Error("user login failed", zap.Error(err))
		return false
	}

	err = DefaultDB.UpdateUser(&p.LbsUser.DBUser)
	if err != nil {
		logger.Error("user login failed", zap.Error(err))
		return false
	}

	a.users[p.UserID] = p
	AskBattleResult(p)
	NoticeLoginOk(p)
	return true
}

func (a *Lbs) OnGetBattleResult(p *LbsPeer, result *BattleResult) {
	js, err := json.Marshal(result)
	if err != nil {
		logger.Error("failed to marshal battle result", zap.Error(err), zap.Any("result", result))
		return
	}

	record, err := DefaultDB.GetBattleRecordUser(result.BattleCode, p.UserID)
	if err != nil {
		logger.Error("failed to load battle record", zap.Error(err), zap.String("record", string(js)))
		return
	}

	record.Round = int(result.BattleCount)
	record.Win = int(result.WinCount)
	record.Lose = int(result.LoseCount)
	record.Kill = int(result.KillCount)
	record.Death = int(result.DeathCount)
	record.Frame = int(result.TotalFrame)
	record.Result = string(js)

	err = DefaultDB.UpdateBattleRecord(record)
	if err != nil {
		logger.Error("failed to save battle record", zap.Error(err), zap.Any("record", record))
		return
	}

	logger.Info("user before battle result", zap.Any("user", p.LbsUser.DBUser))
	rec, err := DefaultDB.CalculateUserTotalBattleCount(p.UserID, 0)
	if err != nil {
		logger.Error("failed to calculate battle count", zap.Error(err))
		return
	}

	p.LbsUser.BattleCount = rec.Battle
	p.LbsUser.WinCount = rec.Win
	p.LbsUser.LoseCount = rec.Lose
	p.LbsUser.KillCount = rec.Kill
	p.LbsUser.DeathCount = rec.Death

	rec, err = DefaultDB.CalculateUserTotalBattleCount(p.UserID, 1)
	if err != nil {
		logger.Error("failed to calculate battle count", zap.Error(err))
		return
	}

	p.LbsUser.AeugBattleCount = rec.Battle
	p.LbsUser.AeugWinCount = rec.Win
	p.LbsUser.AeugLoseCount = rec.Lose
	p.LbsUser.AeugKillCount = rec.Kill
	p.LbsUser.AeugDeathCount = rec.Death

	rec, err = DefaultDB.CalculateUserTotalBattleCount(p.UserID, 2)
	if err != nil {
		logger.Error("failed to calculate battle count", zap.Error(err))
		return
	}

	p.LbsUser.TitansBattleCount = rec.Battle
	p.LbsUser.TitansWinCount = rec.Win
	p.LbsUser.TitansLoseCount = rec.Lose
	p.LbsUser.TitansKillCount = rec.Kill
	p.LbsUser.TitansDeathCount = rec.Death

	rec, err = DefaultDB.CalculateUserDailyBattleCount(p.UserID)
	if err != nil {
		logger.Error("failed to calculate battle count", zap.Error(err))
		return
	}

	p.LbsUser.DailyBattleCount = rec.Battle
	p.LbsUser.DailyWinCount = rec.Win
	p.LbsUser.DailyLoseCount = rec.Lose

	err = DefaultDB.UpdateUser(&p.LbsUser.DBUser)
	if err != nil {
		logger.Error("failed to update user", zap.Error(err))
		return
	}
	logger.Info("user after battle result", zap.Any("user", p.LbsUser.DBUser))
}

type LbsRankingRecord struct {
	Rank        uint32
	EntireCount uint32
	Class       byte
	Battle      uint32
	Win         uint32
	Lose        uint32
	Invalid     uint32
	Kill        uint32
}

func (a *Lbs) getUserRanking(userID string, side byte) *LbsRankingRecord {
	res, err := DefaultDB.CalculateUserTotalBattleCount(userID, side)
	if err != nil {
		logger.Error("failed to update user", zap.Error(err))
	}

	// TODO: Consider a reasonable calculation method.
	c := res.Win / 100
	if 14 <= c {
		c = 14
	}

	return &LbsRankingRecord{
		Rank:        0, // TODO
		EntireCount: 0, // TODO
		Class:       byte(c),
		Battle:      uint32(res.Battle),
		Win:         uint32(res.Win),
		Lose:        uint32(res.Lose),
		Invalid:     uint32(res.Battle - res.Win - res.Lose),
		Kill:        uint32(res.Kill),
	}
}

func (a *Lbs) OnGetUserRanking(p *LbsPeer, kind, page byte) *LbsRankingRecord {
	return a.getUserRanking(p.UserID, page)
}

func (a *Lbs) OnDecideTeam(p *LbsPeer, team string) {
	p.Team = team
	DefaultDB.UpdateUser(&p.LbsUser.DBUser)
}

func (a *Lbs) OnSetUserBinary(p *LbsPeer, bin string) {
	p.Bin = bin
}

func (a *Lbs) OnUserLogout(p *LbsPeer) {
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Battle = nil
	p.Lobby = nil
	p.Room = nil
	p.Entry = EntryNone
	a.removeUser(p)
}

func (a *Lbs) OnUserGotoBattle(p *LbsPeer) {
	if p.Battle != nil {
		a.battles[p.SessionID] = p.Battle
		// The client comes back with this session after the battle.
		Sessions.Touch(p.SessionID)
	}
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Battle = nil
	p.Lobby = nil
	p.Room = nil
	p.Entry = EntryNone
	a.removeUser(p)
}

func (a *Lbs) OnUserTopPageJump(p *LbsPeer) {
	if p.Battle != nil {
		p.Battle = nil
	}
	if p.Room != nil {
		a.OnExitRoom(p)
	}
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Entry = EntryNone
}

// ===========================
// Utility
// ===========================

func (a *Lbs) startTestBattle(platform string, lobbyID uint16, users []*LbsUser) (string, bool) {
	if len(users) != 1 {
		return "ユーザー数エラー", false
	}
	u := users[0]
	peer, ok := a.users[u.UserID]
	if !ok {
		return "ユーザーエラー", false
	}
	ok = time.Since(peer.proxyRegTime).Seconds() <= 20
	if !ok {
		return "UDPプロキシが登録されていません", false
	}
	battle := NewBattle(platform, lobbyID)
	battle.TestBattle = true
	battle.UDPUsers[u.UserID] = true
	battle.P2PMap[u.UserID] = map[string]struct{}{}
	battle.StartTime = time.Now()
	battle.BattleCode = GenBattleCode()

	for _, u := range users {
		battle.Add(u)
		peer, ok := a.users[u.UserID]
		if ok {
			peer.Battle = battle
			NoticeBattleStart(peer)
		}
	}

	return "接続テスト対戦開始", true
}

func (a *Lbs) startBattle(platform string, lobbyID uint16, users []*LbsUser, rule *Rule) bool {
	if a.mcs == nil {
		logger.Error("failed to start battle: no battle server")
		return false
	}

	for _, u := range users {
		_, ok := a.users[u.UserID]
		if !ok {
			return false
		}
	}

	b := NewBattle(platform, lobbyID)
	if rule != nil {
		b.Rule = rule
	}

	// 各クライアントが UDPプロキシの使用と, P2P通信の使用を確定する.
	// 以後 b.UDPUsers, b.P2PMap を正とする.
	for i, u := range users {
		p, ok := a.users[u.UserID]
		if !ok {
			return false
		}
		if time.Since(p.proxyRegTime).Seconds() < 20 {
			p.proxyUseTime = time.Now()
			b.UDPUsers[p.UserID] = true
			b.P2PMap[p.UserID] = map[string]struct{}{}
			if p.proxyP2PConnected == nil {
				p.proxyP2PConnected = map[string]struct{}{}
			}
			for j, other := range users {
				if i == j {
					continue
				}
				_, ok := p.proxyP2PConnected[other.UserID]
				if ok {
					b.P2PMap[p.UserID][other.UserID] = struct{}{}
				}
			}
			logger.Info("zproxy user", zap.String("user_id", p.UserID), zap.Any("p2p_map", b.P2PMap[p.UserID]))
		}
	}

	var battleUsers []McsUser
	for _, u := range users {
		battleUsers = append(battleUsers, McsUser{
			UserID:    u.UserID,
			SessionID: u.SessionID,
			Name:      u.Name,
			Team:      u.Team,
			Entry:     u.Entry,
			P2PMap:    b.P2PMap[u.UserID],
		})
	}
	a.mcs.AddBattle(battleUsers)

	host, port, err := net.SplitHostPort(Conf.Battle.PublicAddr)
	if err != nil {
		logger.Error("battle start failed", zap.Error(err))
		return false
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		logger.Error("battle start failed", zap.Error(err))
		return false
	}

	b.SetBattleServer(net.ParseIP(host), uint16(portNum))
	b.StartTime = time.Now()
	b.BattleCode = GenBattleCode()

	for _, u := range users {
		peer, ok := a.users[u.UserID]
		if ok {
			b.Add(u)
			b.PlatformInfo[u.UserID] = battlePlatformInfo(peer)
			peer.Battle = b
			err = DefaultDB.AddBattleRecord(&BattleRecord{
				BattleCode: b.BattleCode,
				Aggregate:  1,
				UserID:     u.UserID,
				PilotName:  pilotName(u.Bin),
				Players:    len(users),
				Pos:        int(b.GetPosition(u.UserID)),
				Side:       int(u.Entry),
			})
			if err != nil {
				logger.Info("failed to add battle record", zap.Error(err))
				return false
			}
		} else {
			return false
		}
	}

	for _, u := range users {
		peer, ok := a.users[u.UserID]
		if ok {
			NoticeBattleStart(peer)
		}
	}

	return true
}

// ===========================
// Lobby
// ===========================

func (a *Lbs) countBattles(platform string) int {
	count := 0
	for _, battle := range a.battles {
		if battle.Platform == platform {
			count++
		}
	}
	return count
}

func (a *Lbs) OnGetPlazaJoinUser(p *LbsPeer) uint16 {
	return uint16(a.countBattles(p.Platform))
}

func (a *Lbs) noticeLobbyUserCountAll(platform string, lobbyID uint16) {
	l, ok := a.getLobby(platform, lobbyID)
	if ok {
		lb := uint16(len(l.Users))
		bt := a.lobbyBattleCount(platform, lobbyID)
		for _, peer := range a.users {
			if peer.Platform == platform {
				NoticeLobbyUserCount(peer, lobbyID, lb, bt)
			}
		}
	}
}

func (a *Lbs) OnPlatformInfo(p *LbsPeer, info map[string]string) {
	if p.Lobby != nil || p.Room != nil || p.Battle != nil {
		logger.Warn("platform info after lobby entry ignored", zap.String("user_id", p.UserID), zap.Any("info", info))
		return
	}
	p.PlatformInfo = info
	p.Platform = PlatformFromInfo(info)
	logger.Info("platform", zap.Any("platform", p.Platform), zap.Any("info", info))
}

func (a *Lbs) OnEnterLobby(p *LbsPeer, lobbyID uint16) {
	l, ok := a.getLobby(p.Platform, lobbyID)
	if ok {
		p.LbsUser.Sync = ""
		if s := p.PlatformInfo["sync"]; syncFingerprint.MatchString(s) {
			p.LbsUser.Sync = s
		}
		a.dropBattlesOf(p.UserID)
		p.Lobby = l
		p.Lobby.Enter(&p.LbsUser)
		a.noticeLobbyUserCountAll(l.Platform, l.ID)
	}
}

// dropBattlesOf forgets the battles a user left without coming back to the battle after room
// (a new login, a broken battle): the user is no longer in them.
func (a *Lbs) dropBattlesOf(userID string) {
	for sid, battle := range a.battles {
		for _, u := range battle.Users {
			if u.SessionID == sid && u.UserID == userID {
				delete(a.battles, sid)
				if battle.LobbyID != 0 {
					a.noticeLobbyUserCountAll(battle.Platform, battle.LobbyID)
				}
				break
			}
		}
	}
}

func (a *Lbs) OnExitLobby(p *LbsPeer) {
	if p.Room != nil {
		a.OnExitRoom(p)
		p.Room = nil
	}
	if p.Lobby != nil {
		p.Lobby.Exit(p.UserID)
		a.noticeLobbyUserCountAll(p.Lobby.Platform, p.Lobby.ID)
		p.Lobby = nil
	}
}

func (a *Lbs) lobbyBattleCount(platform string, lobbyID uint16) uint16 {
	n := uint16(0)
	for _, battle := range a.battles {
		if battle.Platform == platform && battle.LobbyID == lobbyID {
			n++
		}
	}
	return n
}

func (a *Lbs) OnGetLobbyUserCount(p *LbsPeer, lobbyID uint16) (count, battles uint16) {
	lb, ok := a.getLobby(p.Platform, lobbyID)
	if ok {
		count = uint16(len(lb.Users))
		battles = a.lobbyBattleCount(p.Platform, lobbyID)
	}
	return
}

func (a *Lbs) OnGetLobbyEntryUserCount(p *LbsPeer, lobbyID uint16) (uint16, uint16) {
	l, ok := a.getLobby(p.Platform, lobbyID)
	if !ok {
		return 0, 0
	}
	return l.GetEntryUserCount()
}

func (a *Lbs) OnEntryLobbyBattle(p *LbsPeer, side byte) {
	if p.Lobby != nil {
		lobby := p.Lobby
		lobby.Entry(&p.LbsUser, side)

		setting := lobbySetting(lobby.Platform, lobby.ID)
		if lobbyHasHack(setting, HackZproxyTest) && side != EntryNone {
			users := lobby.StartBattleUsers()
			message, result := a.startTestBattle(lobby.Platform, lobby.ID, users)
			NoticeChatMessage(p, "SERVER", ">", message)
			if result {
				for _, u := range users {
					u.Entry = EntryNone
				}
			} else {
				logger.Error("failed to start battle")
			}
		} else if lobbyCanStart(lobby, setting) {
			users := lobby.StartBattleUsers()
			result := a.startBattle(lobby.Platform, lobby.ID, users, lobbyBattleRule(setting))
			if result {
				for _, u := range users {
					u.Entry = EntryNone
				}
			} else {
				logger.Error("failed to start battle")
			}
		}

		aeug, titans := lobby.GetEntryUserCount()
		for _, u := range lobby.Users {
			peer, ok := a.users[u.UserID]
			if !ok || peer.Room != nil {
				continue
			}
			NoticeLobbyEntryUserCount(peer, lobby.ID, aeug, titans)
		}
	}
}

func (a *Lbs) OnGetBattleUserCount(p *LbsPeer) byte {
	if p.Battle == nil {
		return 0
	}
	return byte(len(p.Battle.Users))
}

func (a *Lbs) OnGetBattleUserPosition(p *LbsPeer) byte {
	if p.Battle == nil {
		return 0
	}
	return p.Battle.GetPosition(p.UserID)
}

func (a *Lbs) OnGetBattleOpponentUser(p *LbsPeer, pos byte) *LbsUser {
	if p.Battle == nil {
		return nil
	}
	return p.Battle.GetUserByPos(pos)
}

// OnGetBattleOpponentClass returns the rank class shown for a user,
// 14 (大将) down to 0 (二等兵).
func (a *Lbs) OnGetBattleOpponentClass(u *LbsUser) uint16 {
	return min(uint16(u.WinCount/100), 14)
}

func (a *Lbs) OnGetBattleRule(p *LbsPeer) *Rule {
	if p.Battle == nil {
		return nil
	}
	return p.Battle.Rule
}

// OnGetBattleCode returns a unique battle code the client is going to join.
// Clients use this value as a random seed.
func (a *Lbs) OnGetBattleCode(p *LbsPeer) (string, error) {
	if p.Battle == nil {
		return "", fmt.Errorf("Battle not found")
	}
	return p.Battle.BattleCode, nil
}

func (a *Lbs) OnGetBattleServerAddress(p *LbsPeer) (net.IP, uint16) {
	if p.Battle == nil {
		return nil, 0
	}
	if p.Battle.UDPUsers[p.UserID] {
		// 本当はUDPUsers確定時に別で記録するべきだが, まあ大丈夫だろう.
		return p.proxyIP, p.proxyPort
	}
	return p.Battle.ServerIP, p.Battle.ServerPort
}

func (a *Lbs) noticeBattleAfterRoomUserCountAll(battle *LbsBattle) uint16 {
	count := uint16(0)
	var peers []*LbsPeer
	for _, u := range battle.Users {
		peer, ok := a.users[u.UserID]
		if ok && peer.inBattleAfterRoom && peer.Battle == battle {
			count++
			peers = append(peers, peer)
		}
	}
	for _, peer := range peers {
		NoticeBattleAfterRoomUserCount(peer, count)
	}
	return count
}

func (a *Lbs) OnEnterBattleAfterRoom(p *LbsPeer) {
	battle, ok := a.battles[p.SessionID]
	if !ok {
		return
	}
	delete(a.battles, p.SessionID)
	if battle.LobbyID != 0 {
		a.noticeLobbyUserCountAll(battle.Platform, battle.LobbyID)
	}
	p.Battle = battle
	p.inBattleAfterRoom = true
	a.noticeBattleAfterRoomUserCountAll(battle)

	count := a.countBattles(battle.Platform)
	for _, peer := range a.users {
		if peer.Lobby == nil && peer.Platform == battle.Platform {
			NoticeBothPlazaJoinUser(peer, 1, uint16(count))
		}
	}
}

func (a *Lbs) OnExitBattleAfterRoom(p *LbsPeer) {
	p.inBattleAfterRoom = false
	if p.Battle == nil {
		return
	}
	battle := p.Battle
	p.Battle = nil
	a.noticeBattleAfterRoomUserCountAll(battle)
}

func (a *Lbs) OnGetBattleAfterRoomUserCount(p *LbsPeer) uint16 {
	if p.Battle == nil {
		return 0
	}
	return a.noticeBattleAfterRoomUserCountAll(p.Battle)
}

// maxChatTextBytes is the longest chat text the game's input sends. Longer text overflows
// the receiving PS2's stack (52 bytes crash it).
const maxChatTextBytes = 40

func chatTextTooLong(s string) bool {
	b, _, _ := transform.String(encoding.ReplaceUnsupported(japanese.ShiftJIS.NewEncoder()), s)
	return len(b) > maxChatTextBytes
}

func (a *Lbs) OnSendChatMessage(p *LbsPeer, msg string) {
	if chatTextTooLong(msg) {
		return
	}
	if p.Lobby != nil && lobbyHasHack(lobbySetting(p.Platform, p.Lobby.ID), HackChatCommand) {
		if lobbyChatCarry(p, msg) {
			return
		}
		LobbyChatHack(p, msg)
	}
	if p.inBattleAfterRoom {
		if p.Battle == nil {
			NoticeChatMessage(p, p.UserID, p.Name, msg)
		} else {
			for _, u := range p.Battle.Users {
				peer, ok := a.users[u.UserID]
				if ok && peer.inBattleAfterRoom && peer.Battle == p.Battle {
					NoticeChatMessage(peer, p.UserID, p.Name, msg)
				}
			}
		}
	} else if p.Room != nil {
		for _, u := range p.Room.Users {
			peer, ok := a.users[u.UserID]
			if !ok {
				continue
			}
			NoticeChatMessage(peer, p.UserID, p.Name, msg)
		}
	} else if p.Lobby != nil {
		for _, u := range p.Lobby.Users {
			peer, ok := a.users[u.UserID]
			if !ok || peer.Room != nil {
				continue
			}
			NoticeChatMessage(peer, p.UserID, p.Name, msg)
		}
	}
}

// ===========================
// Room
// ===========================

func (a *Lbs) OnGetRoomCount(p *LbsPeer) uint16 {
	if p.Lobby != nil {
		return p.Lobby.RoomCount()
	}
	return 0
}

func (a *Lbs) OnGetRoomName(p *LbsPeer, roomID uint16) string {
	if p.Lobby != nil {
		if room, ok := p.Lobby.Rooms[roomID]; ok {
			return room.Name
		}
	}
	return "error"
}

func (a *Lbs) OnGetRoomJoinInfo(p *LbsPeer, roomID uint16) (max uint16) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomID]
		if !ok {
			return
		}
		max = r.MaxPlayer
	}
	return
}

func (a *Lbs) OnGetRoomUserCount(p *LbsPeer, roomID uint16) (count uint16) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomID]
		if !ok {
			return
		}
		count = uint16(len(r.Users))
	}
	return
}

func (a *Lbs) OnGetRoomStatus(p *LbsPeer, roomID uint16) (status byte) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomID]
		if !ok {
			return
		}
		status = r.Status
	}
	return
}

func (a *Lbs) OnGetRoomPasswordInfo(p *LbsPeer, roomID uint16) (pass string, ok bool) {
	//TODO
	ok = false
	return
}

func (a *Lbs) OnRequestCreateRoom(p *LbsPeer, roomID uint16) bool {
	if p.Lobby == nil {
		return false
	}
	r, ok := p.Lobby.Rooms[roomID]
	if !ok {
		return false
	}
	ok = r.Status == RoomStateEmpty
	if ok {
		r.Status = RoomStatePrepare
		p.Room = r

		for id := range p.Lobby.Users {
			peer, ok := a.users[id]
			if !ok {
				continue
			}
			NoticeRoomStatus(peer, r.ID, r.Status)
		}
	}
	return ok
}

func (a *Lbs) OnRequestGetRuleCount(p *LbsPeer, roomID uint16) byte {
	return RuleCount()
}

func (a *Lbs) OnGetNamePermission(p *LbsPeer, roomID uint16) byte {
	return 1 // 0: 不可 1: 可
}

func (a *Lbs) OnGetPasswordPermission(p *LbsPeer, roomID uint16) byte {
	return 0 // 0: 不可 1: 可
}

func (a *Lbs) OnGetRuleName(_ *LbsPeer, _ uint16, ruleID byte) string {
	return RuleTitle(ruleID)
}

func (a *Lbs) OnGetRulePermission(_ *LbsPeer, _ uint16, _ byte) byte {
	return 1
}

func (a *Lbs) OnGetRuleDefaultIndex(p *LbsPeer, roomID uint16, ruleID byte) byte {
	if p.Lobby == nil {
		return 0
	}
	room, ok := p.Lobby.Rooms[roomID]
	if !ok {
		logger.Error("room not found")
		return 0
	}
	return room.Rule.Get(ruleID)
}

func (a *Lbs) OnGetRuleElementName(_ *LbsPeer, _ uint16, ruleID byte, elemID byte) string {
	return RuleElementName(ruleID, elemID)
}

func (a *Lbs) OnGetRuleControl(_ *LbsPeer, _ uint16, _ byte, _ byte) byte {
	//TODO:調査
	return 0
}

func (a *Lbs) OnGetRuleElementCount(_ *LbsPeer, _ uint16, ruleID byte) byte {
	return RuleElementCount(ruleID)
}

func (a *Lbs) OnDecideRoomName(p *LbsPeer, name string) {
	if p.Room == nil {
		return
	}
	p.Room.Name = name
}

func (a *Lbs) OnDecideRoomPassword(p *LbsPeer, pass string) {
	if p.Room == nil {
		return
	}
	p.Room.Password = pass
}

func (a *Lbs) OnDecideRule(p *LbsPeer, ruleID, elemID byte) (nazo byte) {
	if p.Room == nil {
		return
	}
	p.Room.Rule.Set(ruleID, elemID)
	nazo = 1
	return
}
func (a *Lbs) OnDecideRuleFinish(p *LbsPeer) {
	if p.Room == nil || p.Lobby == nil {
		return
	}
	r := p.Room
	r.Enter(&p.LbsUser)
	for id := range p.Lobby.Users {
		peer, ok := a.users[id]
		if !ok {
			continue
		}
		NoticeRoomName(peer, r.ID, r.Name)
		NoticeRoomStatus(peer, r.ID, r.Status)
		NoticeRoomUserCount(peer, r.ID, uint16(len(r.Users)))
		NoticeRoomJoinInfo(peer, r.ID, r.MaxPlayer)
	}
}

func (a *Lbs) OnGetRoomRestTime(p *LbsPeer, roomID uint16) uint16 {
	if p.Lobby == nil {
		return 0
	}
	room, ok := p.Lobby.Rooms[roomID]
	if !ok {
		return 0
	}

	t := room.Deadline.Sub(time.Now()).Seconds()
	if t < 0 {
		t = 0
	}
	return uint16(t)
}

func (a *Lbs) OnGetRoomMember(p *LbsPeer, roomID uint16) (count uint16, users []string) {
	if p.Lobby == nil {
		return
	}
	room, ok := p.Lobby.Rooms[roomID]
	if !ok {
		return
	}
	users = make([]string, 0, 12)
	for _, u := range room.Users {
		users = append(users, u.UserID, u.Name, u.Team)
		count++
	}
	return
}

func (a *Lbs) OnGetRoomEntryList(p *LbsPeer, roomID uint16) (count uint8, ids []string, sides []byte) {
	ids = make([]string, 0, 4)
	sides = make([]byte, 0, 4)
	if p.Lobby == nil {
		return
	}
	room, ok := p.Lobby.Rooms[roomID]
	if !ok {
		return
	}
	for _, u := range room.Users {
		ids = append(ids, u.UserID)
		sides = append(sides, u.Entry)
		count++
	}
	return
}

func (a *Lbs) OnEntryRoomMatch(p *LbsPeer, side byte) {
	if p.Room == nil || p.Lobby == nil {
		return
	}
	p.Room.Entry(&p.LbsUser, side)
	aeug, titans := p.Room.GetEntryUserCount()

	for id := range p.Lobby.Users {
		peer, ok := a.users[id]
		if !ok {
			continue
		}
		NoticeRoomEntry(peer, p.Room.ID, p.UserID, p.Entry)
		NoticeRoomEntryUserCountForLobbyUser(peer, p.Room.ID, aeug, titans)
	}
}

func (a *Lbs) OnEnterRoom(p *LbsPeer, roomID, _, _ uint16) bool {
	if p.Lobby == nil {
		return false
	}
	r, ok := p.Lobby.Rooms[roomID]
	if !ok {
		logger.Error("not found")
		return false
	}
	if r.Status == RoomStateFull {
		return false
	}

	for _, u := range r.Users {
		peer, ok := a.users[u.UserID]
		if !ok {
			continue
		}
		NoticeJoinRoom(peer, p.UserID, p.Name, p.Team)
	}

	r.Enter(&p.LbsUser)
	p.Room = r

	if p.Lobby != nil {
		for id := range p.Lobby.Users {
			peer, ok := a.users[id]
			if !ok {
				continue
			}
			NoticeRoomStatus(peer, r.ID, r.Status)
			NoticeRoomUserCount(peer, r.ID, uint16(len(r.Users)))
			NoticeRoomJoinInfo(peer, r.ID, r.MaxPlayer)
		}
	}
	return true
}

func (a *Lbs) OnExitRoom(p *LbsPeer) {
	if p.Room == nil {
		return
	}
	r := p.Room

	if r.Owner == p.UserID {
		for _, u := range r.Users {
			if r.Owner != u.UserID {
				peer, ok := a.users[u.UserID]
				if !ok {
					continue
				}
				NoticeRemoveRoom(peer)
				peer.Room = nil
				peer.Entry = EntryNone
			}
		}
		r.Remove()
	} else {
		r.Exit(p.UserID)
		for _, u := range r.Users {
			peer, ok := a.users[u.UserID]
			if !ok {
				continue
			}
			NoticeExitRoom(peer, p.UserID, p.Name, p.Team)
		}
	}
	p.Room = nil
	p.Entry = EntryNone

	if p.Lobby != nil {
		for id := range p.Lobby.Users {
			peer, ok := a.users[id]
			if !ok {
				continue
			}
			NoticeRoomName(peer, r.ID, r.Name)
			NoticeRoomStatus(peer, r.ID, r.Status)
			NoticeRoomUserCount(peer, r.ID, uint16(len(r.Users)))
			NoticeRoomJoinInfo(peer, r.ID, r.MaxPlayer)
		}
	}

}

func (a *Lbs) OnGetRoomMatchEntryUserCount(p *LbsPeer, roomID uint16) (aeug uint16, titans uint16) {
	if p.Lobby != nil {
		if room, ok := p.Lobby.Rooms[roomID]; ok {
			return room.GetEntryUserCount()
		}
	}
	return
}

func (a *Lbs) OnNoticeRoomBattleStart(p *LbsPeer) {
	if p.Room == nil {
		return
	}
	if !p.Room.CanBattleStart() {
		return
	}
	active, inactive := p.Room.StartBattleUsers()
	ok := a.startBattle(p.Platform, p.Room.LobbyID, active, p.Room.Rule)
	if ok {
		for _, u := range inactive {
			peer, ok := a.users[u.UserID]
			if ok {
				NoticeRemoveRoom(peer)
			}
		}
	} else {
		logger.Error("failed to start battle")
	}
}

// OnGetRanking returns a page of 10 of the win count (or kill count) ranking for side
// (0 total, 1 AEUG, 2 TITANS) and the last page number.
func (a *Lbs) OnGetRanking(kill bool, side byte, page int) ([]*RankingRecord, int) {
	get := DefaultDB.GetWinCountRanking
	if kill {
		get = DefaultDB.GetKillCountRanking
	}
	ranking, err := get(side)
	if err != nil {
		logger.Error("info message failed", zap.Error(err))
	}
	return rankingPage(ranking, page, 10)
}

func rankingPage(r []*RankingRecord, page int, size int) ([]*RankingRecord, int) {
	lb := page * size
	if lb > len(r) {
		lb = len(r)
	}

	ub := lb + size
	if ub > len(r) {
		ub = len(r)
	}

	maxPage := 0
	if 0 < len(r) {
		n := float64(len(r))
		maxPage = int(math.Ceil(n/float64(size))) - 1
	}
	return r[lb:ub], maxPage
}

// minPcsx2VersionKey: m_server_setting key of the lowest pcsx2 release (zdxsv-X.Y.Z) that may log in.
// Set, it also refuses emulators that send no release version (builds from source, dev builds).
// Read at each login, so a change in the db applies at once.
const minPcsx2VersionKey = "min_pcsx2_version"

// pcsx2AllowKey: m_server_setting key of login keys and machine ids, separated by spaces or commas,
// that skip the version check (development builds on a gated server).
const pcsx2AllowKey = "pcsx2_allow"

func parsePcsx2Version(v string) (semver.Version, error) {
	return semver.Parse(strings.TrimPrefix(v, "zdxsv-"))
}

func pcsx2Allowed(loginKey, machineID string) bool {
	list, err := DefaultDB.GetServerSetting(pcsx2AllowKey)
	if err != nil {
		logger.Warn("GetServerSetting failure", zap.Any("key", pcsx2AllowKey), zap.Error(err))
		return false
	}
	for _, v := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if v == loginKey || (machineID != "" && v == machineID) {
			return true
		}
	}
	return false
}

// pcsx2NeedsUpdate: with a minimum set, an emulator must send a release version (platform info "pcsx2",
// zdxsv-X.Y.Z without a pre-release part) not below it. Consoles and an unset minimum: false.
func pcsx2NeedsUpdate(info map[string]string, loginKey, machineID string) bool {
	if info["emulator"] == "" {
		return false
	}
	minVersion, err := DefaultDB.GetServerSetting(minPcsx2VersionKey)
	if err != nil {
		logger.Warn("GetServerSetting failure", zap.Any("key", minPcsx2VersionKey), zap.Error(err))
		return false
	}
	if minVersion == "" {
		return false
	}
	min, err := parsePcsx2Version(minVersion)
	if err != nil {
		logger.Warn("invalid server setting", zap.Any("key", minPcsx2VersionKey), zap.Any("value", minVersion), zap.Error(err))
		return false
	}
	v, err := parsePcsx2Version(info["pcsx2"])
	if err == nil && len(v.Pre) == 0 && v.GE(min) {
		return false
	}
	return !pcsx2Allowed(loginKey, machineID)
}

func lobbySetting(platform string, lobbyID uint16) *MLobbySetting {
	s, err := DefaultDB.GetLobbySetting(platform, int(lobbyID))
	if err != nil {
		logger.Warn("GetLobbySetting failure", zap.Any("platform", platform), zap.Any("lobby_id", lobbyID), zap.Error(err))
	}
	if s == nil {
		return &MLobbySetting{Platform: platform, No: int(lobbyID)}
	}
	return s
}

// lobbyBattleRule is the m_rule of a battle started in a lobby; nil for the default rule.
func lobbyBattleRule(s *MLobbySetting) *Rule {
	if s.RuleID == "" {
		return nil
	}
	r, err := DefaultDB.GetRule(s.RuleID)
	if err != nil || r == nil {
		logger.Warn("m_lobby_setting rule_id", zap.Any("platform", s.Platform), zap.Any("no", s.No), zap.Any("rule_id", s.RuleID), zap.Any("rule", r), zap.Error(err))
		return nil
	}
	rule := NewRule()
	for k, v := range r.Values() {
		if !rule.SetByKey(k, *v) {
			logger.Warn("m_rule value out of range", zap.Any("rule_id", r.ID), zap.Any("key", k), zap.Any("value", *v))
		}
	}
	return rule
}

func lobbyCanStart(l *LbsLobby, s *MLobbySetting) bool {
	l.Start = s.Start
	return l.CanBattleStart()
}

func lobbyHasHack(s *MLobbySetting, hack string) bool {
	return slices.Contains(strings.Split(s.Hack, ","), hack)
}

func platformHasWideScreen(platform string) bool {
	for i := 0; i < LobbyCount; i++ {
		if lobbyHasHack(lobbySetting(platform, uint16(i)), HackWideScreen) {
			return true
		}
	}
	return false
}

const carryCommand = "ｄｃ"

// SetCarryCodeIssuer turns the carry-over chat command on (chat_command lobbies): issue returns a code that
// links the account to a Discord sign-in in the emulator.
func (a *Lbs) SetCarryCodeIssuer(issue func(loginKey string) string) {
	a.carryCode = issue
}

func lobbyChatCarry(p *LbsPeer, str string) bool {
	if str != carryCommand || p.app.carryCode == nil || p.LoginKey == "" {
		return false
	}
	ban, err := DefaultDB.FindBan(p.LoginKey)
	if err != nil || ban != nil {
		logger.Info("carry code refused", zap.String("login_key", p.LoginKey), zap.Error(err))
		NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコードを発行できません")
		return true
	}
	if n, err := DefaultDB.AccountBattleCount(p.LoginKey); err != nil || n < CarryMinBattles {
		logger.Info("carry code refused: battles", zap.String("login_key", p.LoginKey), zap.Any("battles", n), zap.Error(err))
		NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコードは１００戦以上で発行できます")
		return true
	}
	code := p.app.carryCode(p.LoginKey)
	logger.Info("carry code issued", zap.String("login_key", p.LoginKey))
	NoticeChatMessage(p, "SERVER", "＞", "引き継ぎコード "+code)
	NoticeChatMessage(p, "SERVER", "＞", "３分以内に一度だけ使えます")
	NoticeChatMessage(p, "SERVER", "＞", "他人に教えないでください")
	return true
}
