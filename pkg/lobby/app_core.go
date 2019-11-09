package lobby

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/golang/glog"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"
)

// ===========================
// Login
// ===========================

func (a *App) OnOpen(p *AppPeer) {
	RequestKeyPair(p)
}

func (a *App) OnClose(p *AppPeer) {
	if p.inBattleAfterRoom {
		a.OnExitBattleAfterRoom(p)
	}
	a.OnExitLobby(p)
	delete(a.users, p.UserID)
}

func (a *App) OnKeePair(p *AppPeer, loginKey, sessionID string) {
	ac, err := db.DefaultDB.GetAccountByLoginKey(loginKey)
	if err != nil {
		glog.Errorln(err)
		return
	}

	if ac == nil {
		glog.Errorf("Account not found. loginKey = %v sessionID = %v\n", loginKey, sessionID)
		return
	}

	if ac.SessionID != sessionID {
		glog.Errorln("Mismatch account sessionID, ", ac.SessionID, sessionID)
		return
	}

	p.LoginKey = ac.LoginKey
	p.SessionID = ac.SessionID
	RequestFirstData(p)
}

func (a *App) OnFirstData(p *AppPeer) {
	battle, ok := a.battles[p.SessionID]
	if ok {
		for _, u := range battle.Users {
			if u.SessionID == p.SessionID {
				p.User = u
				break
			}
		}
		p.User.Entry = model.EntryNone
		NoticeUserIDList(p, []*db.User{&p.User.User})
	} else {
		users, err := db.DefaultDB.GetUserList(p.LoginKey)
		if err != nil {
			glog.Errorln(err)
			return
		}
		NoticeUserIDList(p, users)
	}
}

func (a *App) OnDecideUserID(p *AppPeer, userID, name string) {
	sessionID := p.SessionID

	if userID == "******" {
		u, err := db.DefaultDB.RegisterUser(p.LoginKey)
		if err != nil {
			glog.Errorln(err)
			return
		}
		p.User.User = *u
		p.Name = name
		p.SessionID = sessionID
	} else if _, ok := a.battles[p.SessionID]; ok {
		// after battle user
		// do nothing.
	} else if len(name) == 0 || userID == string([]byte{0, 0, 0, 0, 0, 0}) {
		// hmm.. use last login user_id
		ac, err := db.DefaultDB.GetAccountByLoginKey(p.LoginKey)
		if err != nil {
			glog.Errorln(err)
			return
		}
		u, err := db.DefaultDB.GetUser(ac.LastUserID)
		if err != nil {
			glog.Errorln(err)
			return
		}
		p.User.User = *u
		p.SessionID = sessionID
	} else if 0 < len(userID) && 0 < len(name) {
		u, err := db.DefaultDB.GetUser(userID)
		if err != nil {
			glog.Errorln(err)
			return
		}
		p.User.User = *u
		p.Name = name
		p.SessionID = sessionID
	} else {
		glog.Errorln("Undefined UserID:", userID, "Name", name, "SessionID", sessionID)
		return
	}

	err := db.DefaultDB.LoginUser(&p.User.User)
	if err != nil {
		glog.Errorln(err)
		return
	}

	err = db.DefaultDB.UpdateUser(&p.User.User)
	if err != nil {
		glog.Errorln(err)
		return
	}

	a.users[p.UserID] = p
	AskBattleResult(p)
	NoticeLoginOk(p)
}

func (a *App) OnGetBattleResult(p *AppPeer, result *model.BattleResult) {
	js, err := json.Marshal(result)
	if err != nil {
		glog.Errorln("Failed to marshal battle result", err)
		glog.Infoln(result)
		return
	}

	record, err := db.DefaultDB.GetBattleRecordUser(result.BattleCode, p.UserID)
	if err != nil {
		glog.Errorln("Failed to load battle record", err)
		glog.Infoln(string(js))
		return
	}

	record.Round = int(result.BattleCount)
	record.Win = int(result.WinCount)
	record.Lose = int(result.LoseCount)
	record.Kill = int(result.KillCount)
	record.Death = int(result.DeathCount)
	record.Frame = int(result.TotalFrame)
	record.Result = string(js)

	err = db.DefaultDB.UpdateBattleRecord(record)
	if err != nil {
		glog.Errorln("Failed to save battle record", err)
		glog.Infoln(record)
		return
	}

	glog.Infoln("before", p.User.User)
	rec, err := db.DefaultDB.CalculateUserTotalBattleCount(p.UserID, 0)
	if err != nil {
		glog.Errorln("Failed to calculate battle count", err)
		return
	}

	p.User.BattleCount = rec.Battle
	p.User.WinCount = rec.Win
	p.User.LoseCount = rec.Lose
	p.User.KillCount = rec.Kill
	p.User.DeathCount = rec.Death

	rec, err = db.DefaultDB.CalculateUserDailyBattleCount(p.UserID)
	if err != nil {
		glog.Errorln("Failed to calculate battle count", err)
		return
	}

	p.User.DailyBattleCount = rec.Battle
	p.User.DailyWinCount = rec.Win
	p.User.DailyLoseCount = rec.Lose

	err = db.DefaultDB.UpdateUser(&p.User.User)
	if err != nil {
		glog.Errorln(err)
		return
	}
	glog.Infoln("after", p.User.User)
}

type RankingRecord struct {
	Rank        uint32
	EntireCount uint32
	Class       byte
	Battle      uint32
	Win         uint32
	Lose        uint32
	Invalid     uint32
	Kill        uint32
}

func (a *App) getUserRanking(userID string, side byte) *RankingRecord {
	res, err := db.DefaultDB.CalculateUserTotalBattleCount(userID, side)
	if err != nil {
		glog.Errorln(err)
	}

	// TODO: Consider a reasonable calculation method.
	c := res.Win / 100
	if 14 <= c {
		c = 14
	}

	return &RankingRecord{
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

func (a *App) OnGetUserRanking(p *AppPeer, kind, page byte) *RankingRecord {
	return a.getUserRanking(p.UserID, page)
}

func (a *App) OnDecideTeam(p *AppPeer, team string) {
	p.Team = team
	db.DefaultDB.UpdateUser(&p.User.User)
}

func (a *App) OnSetUserBinary(p *AppPeer, bin string) {
	p.Bin = bin
}

func (a *App) OnUserLogout(p *AppPeer) {
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Battle = nil
	p.Lobby = nil
	p.Room = nil
	p.Entry = model.EntryNone
	delete(a.users, p.UserID)
}

func (a *App) OnUserGotoBattle(p *AppPeer) {
	if p.Battle != nil {
		a.battles[p.SessionID] = p.Battle
	}
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Battle = nil
	p.Lobby = nil
	p.Room = nil
	p.Entry = model.EntryNone
	delete(a.users, p.UserID)
}

func (a *App) OnUserTopPageJump(p *AppPeer) {
	if p.Battle != nil {
		p.Battle = nil // TODO これでいいのか
	}
	if p.Room != nil {
		a.OnExitRoom(p)
	}
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Entry = model.EntryNone
}

// ===========================
// Utility
// ===========================

func (a *App) startTestBattle(lobbyID uint16, users []*model.User) (string, bool) {
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
	battle := model.NewBattle(lobbyID)
	battle.TestBattle = true
	battle.UDPUsers[u.UserID] = true
	battle.P2PMap[u.UserID] = map[string]struct{}{}
	battle.StartTime = time.Now()
	battle.BattleCode = db.GenBattleCode()

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

func (a *App) startBattle(lobbyID uint16, users []*model.User, rule *model.Rule) bool {
	if a.battleServer == nil {
		glog.Errorln("Failed to battle start because App.battleServer is nil")
		return false
	}

	for _, u := range users {
		_, ok := a.users[u.UserID]
		if !ok {
			return false
		}
	}

	battle := model.NewBattle(lobbyID)
	if rule != nil {
		battle.Rule = rule
	}

	// 各クライアントが UDPプロキシの使用と, P2P通信の使用を確定する.
	// 以後 battle.UDPUsers, battle.P2PMap を正とする.
	for i, u := range users {
		p, ok := a.users[u.UserID]
		if !ok {
			return false
		}
		if time.Since(p.proxyRegTime).Seconds() < 20 {
			p.proxyUseTime = time.Now()
			battle.UDPUsers[p.UserID] = true
			battle.P2PMap[p.UserID] = map[string]struct{}{}
			if p.proxyP2PConnected == nil {
				p.proxyP2PConnected = map[string]struct{}{}
			}
			for j, other := range users {
				if i == j {
					continue
				}
				_, ok := p.proxyP2PConnected[other.UserID]
				if ok {
					battle.P2PMap[p.UserID][other.UserID] = struct{}{}
				}
			}
			glog.Infoln("zproxy user", p.UserID, "P2PMap", battle.P2PMap[p.UserID])
		}
	}

	var reply int
	args := &battlerpc.BattleInfoArgs{}
	for _, u := range users {
		args.Users = append(args.Users, battlerpc.User{
			UserID:    u.UserID,
			SessionID: u.SessionID,
			Name:      u.Name,
			Team:      u.Team,
			Entry:     u.Entry,
			P2PMap:    battle.P2PMap[u.UserID],
		})
	}

	err := a.battleServer.Call("Logic.NotifyBattleUsers", args, &reply)
	if err != nil {
		glog.Errorln("Logic.NotifyBattleUsers failed. ", err)
		return false
	}

	host, port, err := net.SplitHostPort(config.Conf.Battle.PublicAddr)
	if err != nil {
		glog.Errorln(err)
		return false
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		glog.Errorln(err)
		return false
	}

	battle.SetBattleServer(net.ParseIP(host), uint16(portNum))
	battle.StartTime = time.Now()
	battle.BattleCode = db.GenBattleCode()

	for _, u := range users {
		peer, ok := a.users[u.UserID]
		if ok {
			battle.Add(u)
			peer.Battle = battle
			err = db.DefaultDB.AddBattleRecord(&db.BattleRecord{
				BattleCode: battle.BattleCode,
				Aggregate:  1,
				UserID:     u.UserID,
				Players:    len(users),
				Pos:        int(battle.GetPosition(u.UserID)),
				Side:       int(u.Entry),
			})
			if err != nil {
				glog.Infoln("Failed to add battle record", err)
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

func (a *App) OnGetPlazaJoinUser() uint16 {
	return uint16(len(a.battles))
}

func (a *App) noticeLobbyUserCountAll(lobbyID uint16) {
	l, ok := a.lobbys[lobbyID]
	if ok {
		lb := uint16(len(l.Users))
		bt := uint16(0)
		for _, battle := range a.battles {
			if battle.LobbyID == lobbyID {
				bt++
			}
		}
		for _, peer := range a.users {
			NoticeLobbyUserCount(peer, lobbyID, lb, bt)
		}
	}
}

func (a *App) OnEnterLobby(p *AppPeer, lobbyID uint16) {
	l, ok := a.lobbys[lobbyID]
	if ok {
		p.Lobby = l
		p.Lobby.Enter(&p.User)
		a.noticeLobbyUserCountAll(p.Lobby.ID)
	}
}

func (a *App) OnExitLobby(p *AppPeer) {
	if p.Room != nil {
		a.OnExitRoom(p)
		p.Room = nil
	}
	if p.Lobby != nil {
		p.Lobby.Exit(p.UserID)
		a.noticeLobbyUserCountAll(p.Lobby.ID)
		p.Lobby = nil
	}
}

func (a *App) OnGetLobbyUserCount(p *AppPeer, lobbyID uint16) (count uint16) {
	lb, ok := a.lobbys[lobbyID]
	if ok {
		count = uint16(len(lb.Users))
	}
	return
}

func (a *App) OnGetLobbyEntryUserCount(p *AppPeer, lobbyID uint16) (uint16, uint16) {
	l, ok := a.lobbys[lobbyID]
	if !ok {
		return 0, 0
	}
	return l.GetEntryUserCount()
}

func (a *App) OnEntryLobbyBattle(p *AppPeer, side byte) {
	if p.Lobby != nil {
		lobby := p.Lobby
		lobby.Entry(&p.User, side)

		if lobby.ID == uint16(1) && side != model.EntryNone {
			users := lobby.StartBattleUsers()
			message, result := a.startTestBattle(lobby.ID, users)
			NoticeChatMessage(p, "SERVER", ">", message)
			if result {
				for _, u := range users {
					u.Entry = model.EntryNone
				}
			} else {
				glog.Errorln("Failed to start battle")
			}
		} else if lobby.CanBattleStart() {
			users := lobby.StartBattleUsers()
			result := a.startBattle(lobby.ID, users, nil)
			if result {
				for _, u := range users {
					u.Entry = model.EntryNone
				}
			} else {
				glog.Errorln("Failed to start battle")
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

func (a *App) OnGetBattleUserCount(p *AppPeer) byte {
	if p.Battle == nil {
		return 0
	}
	return byte(len(p.Battle.Users))
}

func (a *App) OnGetBattleUserPosition(p *AppPeer) byte {
	if p.Battle == nil {
		return 0
	}
	return p.Battle.GetPosition(p.UserID)
}

func (a *App) OnGetBattleOpponentUser(p *AppPeer, pos byte) *model.User {
	if p.Battle == nil {
		return nil
	}
	return p.Battle.GetUserByPos(pos)
}

// var _ = register(0x6917, "GetBattleOpponentStatus", func(p *AppPeer, m *Message) {

func (a *App) OnGetBattleRule(p *AppPeer) *model.Rule {
	if p.Battle == nil {
		return nil
	}
	return p.Battle.Rule
}

// OnGetBattleCode returns a unique battle code the client is going to join.
// Clients use this value as a random seed.
func (a *App) OnGetBattleCode(p *AppPeer) (string, error) {
	if p.Battle == nil {
		return "", fmt.Errorf("Battle not found")
	}
	return p.Battle.BattleCode, nil
}

func (a *App) OnGetBattleServerAddress(p *AppPeer) (net.IP, uint16) {
	if p.Battle == nil {
		return nil, 0
	}
	if p.Battle.UDPUsers[p.UserID] {
		// 本当はUDPUsers確定時に別で記録するべきだが, まあ大丈夫だろう.
		return p.proxyIP, p.proxyPort
	}
	return p.Battle.ServerIP, p.Battle.ServerPort
}

func (a *App) noticeBattleAfterRoomUserCountAll(battle *model.Battle) uint16 {
	count := uint16(0)
	var peers []*AppPeer
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

func (a *App) OnEnterBattleAfterRoom(p *AppPeer) {
	battle, ok := a.battles[p.SessionID]
	if !ok {
		return
	}
	delete(a.battles, p.SessionID)
	if battle.LobbyID != 0 {
		a.noticeLobbyUserCountAll(battle.LobbyID)
	}
	p.Battle = battle
	p.inBattleAfterRoom = true
	a.noticeBattleAfterRoomUserCountAll(battle)

	count := len(a.battles)
	for _, peer := range a.users {
		if peer.Lobby == nil {
			NoticeBothPlazaJoinUser(peer, 1, uint16(count))
		}
	}
}

func (a *App) OnExitBattleAfterRoom(p *AppPeer) {
	p.inBattleAfterRoom = false
	if p.Battle == nil {
		return
	}
	battle := p.Battle
	p.Battle = nil
	a.noticeBattleAfterRoomUserCountAll(battle)
}

func (a *App) OnGetBattleAfterRoomUserCount(p *AppPeer) uint16 {
	if p.Battle == nil {
		return 0
	}
	return a.noticeBattleAfterRoomUserCountAll(p.Battle)
}

func (a *App) OnSendChatMessage(p *AppPeer, msg string) {
	if p.inBattleAfterRoom {
		if p.Battle == nil {
			NoticeChatMessage(p, p.UserID, p.Name, msg)
		} else {
			for _, u := range p.Battle.Users {
				peer, ok := a.users[u.UserID]
				if ok && p.inBattleAfterRoom && peer.Battle == p.Battle {
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

func (a *App) OnGetRoomCount(p *AppPeer) uint16 {
	if p.Lobby != nil {
		return p.Lobby.RoomCount()
	}
	return 0
}

func (a *App) OnGetRoomName(p *AppPeer, roomID uint16) string {
	if p.Lobby != nil {
		if room, ok := p.Lobby.Rooms[roomID]; ok {
			return room.Name
		}
	}
	return "error"
}

func (a *App) OnGetRoomJoinInfo(p *AppPeer, roomID uint16) (max uint16) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomID]
		if !ok {
			return
		}
		max = r.MaxPlayer
	}
	return
}

func (a *App) OnGetRoomUserCount(p *AppPeer, roomID uint16) (count uint16) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomID]
		if !ok {
			return
		}
		count = uint16(len(r.Users))
	}
	return
}

func (a *App) OnGetRoomStatus(p *AppPeer, roomID uint16) (status byte) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomID]
		if !ok {
			return
		}
		status = r.Status
	}
	return
}

func (a *App) OnGetRoomPasswordInfo(p *AppPeer, roomID uint16) (pass string, ok bool) {
	//TODO
	ok = false
	return
}

func (a *App) OnRequestCreateRoom(p *AppPeer, roomID uint16) bool {
	if p.Lobby == nil {
		return false
	}
	r, ok := p.Lobby.Rooms[roomID]
	if !ok {
		return false
	}
	ok = r.Status == model.RoomStateEmpty
	if ok {
		r.Status = model.RoomStatePrepare
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

func (a *App) OnRequestGetRuleCount(p *AppPeer, roomID uint16) byte {
	return model.RuleCount()
}

func (a *App) OnGetNamePermission(p *AppPeer, roomID uint16) byte {
	return 1 // 0: 不可 1: 可
}

func (a *App) OnGetPasswordPermission(p *AppPeer, roomID uint16) byte {
	return 0 // 0: 不可 1: 可
}

func (a *App) OnGetRuleName(_ *AppPeer, _ uint16, ruleID byte) string {
	return model.RuleTitle(ruleID)
}

func (a *App) OnGetRulePermission(_ *AppPeer, _ uint16, _ byte) byte {
	return 1
}

func (a *App) OnGetRuleDefaultIndex(p *AppPeer, roomID uint16, ruleID byte) byte {
	room, ok := p.Lobby.Rooms[roomID]
	if !ok {
		glog.Errorln("room not found")
		return 0
	}
	return room.Rule.Get(ruleID)
}

func (a *App) OnGetRuleElementName(_ *AppPeer, _ uint16, ruleID byte, elemID byte) string {
	return model.RuleElementName(ruleID, elemID)
}

func (a *App) OnGetRuleControl(_ *AppPeer, _ uint16, _ byte, _ byte) byte {
	//TODO:調査
	return 0
}

func (a *App) OnGetRuleElementCount(_ *AppPeer, _ uint16, ruleID byte) byte {
	return model.RuleElementCount(ruleID)
}

func (a *App) OnDecideRoomName(p *AppPeer, name string) {
	p.Room.Name = name
}

func (a *App) OnDecideRoomPassword(p *AppPeer, pass string) {
	p.Room.Password = pass
}

func (a *App) OnDecideRule(p *AppPeer, ruleID, elemID byte) (nazo byte) {
	p.Room.Rule.Set(ruleID, elemID)
	nazo = 1
	return
}
func (a *App) OnDecideRuleFinish(p *AppPeer) {
	r := p.Room
	r.Enter(&p.User)
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

func (a *App) OnGetRoomRestTime(p *AppPeer, roomID uint16) uint16 {
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

func (a *App) OnGetRoomMember(p *AppPeer, roomID uint16) (count uint16, users []string) {
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

func (a *App) OnGetRoomEntryList(p *AppPeer, roomID uint16) (count uint8, ids []string, sides []byte) {
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

func (a *App) OnEntryRoomMatch(p *AppPeer, side byte) {
	p.Room.Entry(&p.User, side)
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

func (a *App) OnEnterRoom(p *AppPeer, roomID, _, _ uint16) bool {
	r, ok := p.Lobby.Rooms[roomID]
	if !ok {
		glog.Errorln("Not found")
		return false
	}
	if r.Status == model.RoomStateFull {
		return false
	}

	for _, u := range r.Users {
		peer, ok := a.users[u.UserID]
		if !ok {
			continue
		}
		NoticeJoinRoom(peer, p.UserID, p.Name, p.Team)
	}

	r.Enter(&p.User)
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

func (a *App) OnExitRoom(p *AppPeer) {
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
				peer.Entry = model.EntryNone
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
	p.Entry = model.EntryNone

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

func (a *App) OnGetRoomMatchEntryUserCount(p *AppPeer, roomID uint16) (aeug uint16, titans uint16) {
	if p.Lobby != nil {
		if room, ok := p.Lobby.Rooms[roomID]; ok {
			return room.GetEntryUserCount()
		}
	}
	return
}

func (a *App) OnNoticeRoomBattleStart(p *AppPeer) {
	if p.Room == nil {
		return
	}
	if !p.Room.CanBattleStart() {
		return
	}
	active, inactive := p.Room.StartBattleUsers()
	ok := a.startBattle(p.Room.LobbyID, active, p.Room.Rule)
	if ok {
		for _, u := range inactive {
			peer, ok := a.users[u.UserID]
			if ok {
				NoticeRemoveRoom(peer)
			}
		}
	} else {
		glog.Errorln("Failed to start battle")
	}
}
