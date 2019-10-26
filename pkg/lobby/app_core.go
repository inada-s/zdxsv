package lobby

import (
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
	delete(a.users, p.UserId)
}

func (a *App) OnKeePair(p *AppPeer, loginKey, sessionId string) {
	ac, err := db.DefaultDB.GetAccountByLoginKey(loginKey)
	if err != nil {
		glog.Errorln(err)
		return
	}

	if ac == nil {
		glog.Errorf("Account not found. loginKey = %v sessionId = %v\n", loginKey, sessionId)
		return
	}

	if ac.SessionId != sessionId {
		glog.Errorln("Mismatch account sessionId, ", ac.SessionId, sessionId)
		return
	}

	p.LoginKey = ac.LoginKey
	p.SessionId = ac.SessionId
	RequestFirstData(p)
}

func (a *App) OnFirstData(p *AppPeer) {
	battle, ok := a.battles[p.SessionId]
	if ok {
		for _, u := range battle.Users {
			if u.SessionId == p.SessionId {
				p.User = u
				break
			}
		}
		p.User.Entry = model.EntryNone
		NoticeUserIdList(p, []*db.User{&p.User.User})
	} else {
		users, err := db.DefaultDB.GetUserList(p.LoginKey)
		if err != nil {
			glog.Errorln(err)
			return
		}
		NoticeUserIdList(p, users)
	}
}

func (a *App) OnDecideUserId(p *AppPeer, userId, name string) {
	sessionId := p.SessionId

	if userId == "******" {
		u, err := db.DefaultDB.RegisterUser(p.LoginKey)
		if err != nil {
			glog.Errorln(err)
			return
		}
		p.User.User = *u
		p.Name = name
		p.SessionId = sessionId
	} else if _, ok := a.battles[p.SessionId]; ok {
		// after battle user
		// do nothing.
	} else if len(name) == 0 || userId == string([]byte{0, 0, 0, 0, 0, 0}) {
		// hmm.. use last login user_id
		ac, err := db.DefaultDB.GetAccountByLoginKey(p.LoginKey)
		if err != nil {
			glog.Errorln(err)
			return
		}
		u, err := db.DefaultDB.GetUser(ac.LastUserId)
		if err != nil {
			glog.Errorln(err)
			return
		}
		p.User.User = *u
		p.SessionId = sessionId
	} else if 0 < len(userId) && 0 < len(name) {
		u, err := db.DefaultDB.GetUser(userId)
		if err != nil {
			glog.Errorln(err)
			return
		}
		p.User.User = *u
		p.Name = name
		p.SessionId = sessionId
	} else {
		glog.Errorln("Undefined UserId:", userId, "Name", name, "SessionId", sessionId)
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
	a.users[p.UserId] = p
	NoticeLoginOk(p)
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
	delete(a.users, p.UserId)
}

func (a *App) OnUserGotoBattle(p *AppPeer) {
	if p.Battle != nil {
		a.battles[p.SessionId] = p.Battle
	}
	if p.Lobby != nil {
		a.OnExitLobby(p)
	}
	p.Battle = nil
	p.Lobby = nil
	p.Room = nil
	p.Entry = model.EntryNone
	delete(a.users, p.UserId)
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

func (a *App) startTestBattle(lobbyId uint16, users []*model.User) (string, bool) {
	if len(users) != 1 {
		return "ユーザー数エラー", false
	}
	u := users[0]
	peer, ok := a.users[u.UserId]
	if !ok {
		return "ユーザーエラー", false
	}
	ok = time.Since(peer.proxyRegTime).Seconds() <= 20
	if !ok {
		return "UDPプロキシが登録されていません", false
	}
	battle := model.NewBattle(lobbyId)
	battle.TestBattle = true
	battle.UDPUsers[u.UserId] = true
	battle.P2PMap[u.UserId] = map[string]struct{}{}
	battle.StartTime = time.Now()
	for _, u := range users {
		battle.Add(u)
		peer, ok := a.users[u.UserId]
		if ok {
			peer.Battle = battle
			NoticeBattleStart(peer)
		}
	}
	return "接続テスト対戦開始", true
}

func (a *App) startBattle(lobbyId uint16, users []*model.User, rule *model.Rule) bool {
	if a.battleServer == nil {
		glog.Errorln("Failed to battle start because App.battleServer is nil")
		return false
	}

	for _, u := range users {
		_, ok := a.users[u.UserId]
		if !ok {
			return false
		}
	}

	battle := model.NewBattle(lobbyId)
	if rule != nil {
		battle.Rule = rule
	}

	// 各クライアントが UDPプロキシの使用と, P2P通信の使用を確定する.
	// 以後 battle.UDPUsers, battle.P2PMap を正とする.
	for i, u := range users {
		p, ok := a.users[u.UserId]
		if !ok {
			return false
		}
		if time.Since(p.proxyRegTime).Seconds() < 20 {
			p.proxyUseTime = time.Now()
			battle.UDPUsers[p.UserId] = true
			battle.P2PMap[p.UserId] = map[string]struct{}{}
			if p.proxyP2PConnected == nil {
				p.proxyP2PConnected = map[string]struct{}{}
			}
			for j, other := range users {
				if i == j {
					continue
				}
				_, ok := p.proxyP2PConnected[other.UserId]
				if ok {
					battle.P2PMap[p.UserId][other.UserId] = struct{}{}
				}
			}
			glog.Infoln("zproxy user", p.UserId, "P2PMap", battle.P2PMap[p.UserId])
		}
	}

	var reply int
	args := &battlerpc.BattleInfoArgs{}
	for _, u := range users {
		args.Users = append(args.Users, battlerpc.User{
			UserId:    u.UserId,
			SessionId: u.SessionId,
			Name:      u.Name,
			Team:      u.Team,
			Entry:     u.Entry,
			P2PMap:    battle.P2PMap[u.UserId],
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

	for _, u := range users {
		battle.Add(u)
		peer, ok := a.users[u.UserId]
		if ok {
			peer.Battle = battle
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

func (a *App) noticeLobbyUserCountAll(lobbyId uint16) {
	l, ok := a.lobbys[lobbyId]
	if ok {
		lb := uint16(len(l.Users))
		bt := uint16(0)
		for _, battle := range a.battles {
			if battle.LobbyId == lobbyId {
				bt++
			}
		}
		for _, peer := range a.users {
			NoticeLobbyUserCount(peer, lobbyId, lb, bt)
		}
	}
}

func (a *App) OnEnterLobby(p *AppPeer, lobbyId uint16) {
	l, ok := a.lobbys[lobbyId]
	if ok {
		p.Lobby = l
		p.Lobby.Enter(&p.User)
		a.noticeLobbyUserCountAll(p.Lobby.Id)
	}
}

func (a *App) OnExitLobby(p *AppPeer) {
	if p.Room != nil {
		a.OnExitRoom(p)
		p.Room = nil
	}
	if p.Lobby != nil {
		p.Lobby.Exit(p.UserId)
		a.noticeLobbyUserCountAll(p.Lobby.Id)
		p.Lobby = nil
	}
}

func (a *App) OnGetLobbyUserCount(p *AppPeer, lobbyId uint16) (count uint16) {
	lb, ok := a.lobbys[lobbyId]
	if ok {
		count = uint16(len(lb.Users))
	}
	return
}

func (a *App) OnGetLobbyEntryUserCount(p *AppPeer, lobbyId uint16) (uint16, uint16) {
	l, ok := a.lobbys[lobbyId]
	if !ok {
		return 0, 0
	}
	return l.GetEntryUserCount()
}

func (a *App) OnEntryLobbyBattle(p *AppPeer, side byte) {
	if p.Lobby != nil {
		lobby := p.Lobby
		lobby.Entry(&p.User, side)

		if lobby.Id == uint16(1) && side != model.EntryNone {
			users := lobby.StartBattleUsers()
			message, result := a.startTestBattle(lobby.Id, users)
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
			result := a.startBattle(lobby.Id, users, nil)
			if result {
				for _, u := range users {
					u.Entry = model.EntryNone
				}
			} else {
				glog.Errorln("Failed to start battle")
			}
		}

		aeug, titans := lobby.GetEntryUserCount()
		msg := fmt.Sprintf("エゥーゴx%d ティターンズx%d", aeug, titans)
		for _, u := range lobby.Users {
			peer, ok := a.users[u.UserId]
			if !ok || peer.Room != nil {
				continue
			}
			NoticeChatMessage(peer, "SERVER", ">", msg)
			// NoticeEntryUserCount(peer, lobby.Id, aeug, titans)
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
	return p.Battle.GetPosition(p.UserId)
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

// var _ = register(0x6915, "GetBattleBattleCode", func(p *AppPeer, m *Message) {

func (a *App) OnGetBattleServerAddress(p *AppPeer) (net.IP, uint16) {
	if p.Battle == nil {
		return nil, 0
	}
	if p.Battle.UDPUsers[p.UserId] {
		// 本当はUDPUsers確定時に別で記録するべきだが, まあ大丈夫だろう.
		return p.proxyIP, p.proxyPort
	}
	return p.Battle.ServerIP, p.Battle.ServerPort
}

func (a *App) noticeBattleAfterRoomUserCountAll(battle *model.Battle) uint16 {
	count := uint16(0)
	var peers []*AppPeer
	for _, u := range battle.Users {
		peer, ok := a.users[u.UserId]
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
	battle, ok := a.battles[p.SessionId]
	if !ok {
		return
	}
	delete(a.battles, p.SessionId)
	if battle.LobbyId != 0 {
		a.noticeLobbyUserCountAll(battle.LobbyId)
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
			NoticeChatMessage(p, p.UserId, p.Name, msg)
		} else {
			for _, u := range p.Battle.Users {
				peer, ok := a.users[u.UserId]
				if ok && p.inBattleAfterRoom && peer.Battle == p.Battle {
					NoticeChatMessage(peer, p.UserId, p.Name, msg)
				}
			}
		}
	} else if p.Room != nil {
		for _, u := range p.Room.Users {
			peer, ok := a.users[u.UserId]
			if !ok {
				continue
			}
			NoticeChatMessage(peer, p.UserId, p.Name, msg)
		}
	} else if p.Lobby != nil {
		for _, u := range p.Lobby.Users {
			peer, ok := a.users[u.UserId]
			if !ok || peer.Room != nil {
				continue
			}
			NoticeChatMessage(peer, p.UserId, p.Name, msg)
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

func (a *App) OnGetRoomName(p *AppPeer, roomId uint16) string {
	if p.Lobby != nil {
		if room, ok := p.Lobby.Rooms[roomId]; ok {
			return room.Name
		}
	}
	return "error"
}

func (a *App) OnGetRoomJoinInfo(p *AppPeer, roomId uint16) (max uint16) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomId]
		if !ok {
			return
		}
		max = r.MaxPlayer
	}
	return
}

func (a *App) OnGetRoomUserCount(p *AppPeer, roomId uint16) (count uint16) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomId]
		if !ok {
			return
		}
		count = uint16(len(r.Users))
	}
	return
}

func (a *App) OnGetRoomStatus(p *AppPeer, roomId uint16) (status byte) {
	if p.Lobby != nil {
		r, ok := p.Lobby.Rooms[roomId]
		if !ok {
			return
		}
		status = r.Status
	}
	return
}

func (a *App) OnGetRoomPasswordInfo(p *AppPeer, roomId uint16) (pass string, ok bool) {
	//TODO
	ok = false
	return
}

func (a *App) OnRequestCreateRoom(p *AppPeer, roomId uint16) bool {
	if p.Lobby == nil {
		return false
	}
	r, ok := p.Lobby.Rooms[roomId]
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
			NoticeRoomStatus(peer, r.Id, r.Status)
		}
	}
	return ok
}

func (a *App) OnRequestGetRuleCount(p *AppPeer, roomId uint16) byte {
	return model.RuleCount()
}

func (a *App) OnGetNamePermission(p *AppPeer, roomId uint16) byte {
	return 1 // 0: 不可 1: 可
}

func (a *App) OnGetPasswordPermission(p *AppPeer, roomId uint16) byte {
	return 0 // 0: 不可 1: 可
}

func (a *App) OnGetRuleName(_ *AppPeer, _ uint16, ruleId byte) string {
	return model.RuleTitle(ruleId)
}

func (a *App) OnGetRulePermission(_ *AppPeer, _ uint16, _ byte) byte {
	return 1
}

func (a *App) OnGetRuleDefaultIndex(p *AppPeer, roomId uint16, ruleId byte) byte {
	room, ok := p.Lobby.Rooms[roomId]
	if !ok {
		glog.Errorln("room not found")
		return 0
	}
	return room.Rule.Get(ruleId)
}

func (a *App) OnGetRuleElementName(_ *AppPeer, _ uint16, ruleId byte, elemId byte) string {
	return model.RuleElementName(ruleId, elemId)
}

func (a *App) OnGetRuleControl(_ *AppPeer, _ uint16, _ byte, _ byte) byte {
	//TODO:調査
	return 0
}

func (a *App) OnGetRuleElementCount(_ *AppPeer, _ uint16, ruleId byte) byte {
	return model.RuleElementCount(ruleId)
}

func (a *App) OnDecideRoomName(p *AppPeer, name string) {
	p.Room.Name = name
}

func (a *App) OnDecideRoomPassword(p *AppPeer, pass string) {
	p.Room.Password = pass
}

func (a *App) OnDecideRule(p *AppPeer, ruleId, elemId byte) (nazo byte) {
	p.Room.Rule.Set(ruleId, elemId)
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
		NoticeRoomName(peer, r.Id, r.Name)
		NoticeRoomStatus(peer, r.Id, r.Status)
		NoticeRoomUserCount(peer, r.Id, uint16(len(r.Users)))
		NoticeRoomJoinInfo(peer, r.Id, r.MaxPlayer)
	}
}

func (a *App) OnGetRoomRestTime(p *AppPeer, roomId uint16) uint16 {
	if p.Lobby == nil {
		return 0
	}
	room, ok := p.Lobby.Rooms[roomId]
	if !ok {
		return 0
	}

	t := room.Deadline.Sub(time.Now()).Seconds()
	if t < 0 {
		t = 0
	}
	return uint16(t)
}

func (a *App) OnGetRoomMember(p *AppPeer, roomId uint16) (count uint16, users []string) {
	if p.Lobby == nil {
		return
	}
	room, ok := p.Lobby.Rooms[roomId]
	if !ok {
		return
	}
	users = make([]string, 0, 12)
	for _, u := range room.Users {
		users = append(users, u.UserId, u.Name, u.Team)
		count++
	}
	return
}

func (a *App) OnGetRoomEntryList(p *AppPeer, roomId uint16) (count uint8, ids []string, sides []byte) {
	ids = make([]string, 0, 4)
	sides = make([]byte, 0, 4)
	if p.Lobby == nil {
		return
	}
	room, ok := p.Lobby.Rooms[roomId]
	if !ok {
		return
	}
	for _, u := range room.Users {
		ids = append(ids, u.UserId)
		sides = append(sides, u.Entry)
		count++
	}
	return
}

func (a *App) OnEntryRoomMatch(p *AppPeer, side byte) {
	p.Room.Entry(&p.User, side)

	for id := range p.Lobby.Users {
		peer, ok := a.users[id]
		if !ok {
			continue
		}
		NoticeRoomEntry(peer, p.Room.Id, p.UserId, p.Entry)
	}

	aeug, titans := p.Room.GetEntryUserCount()
	msg := fmt.Sprintf("エゥーゴx%d ティターンズx%d", aeug, titans)
	for _, u := range p.Room.Users {
		peer, ok := a.users[u.UserId]
		if !ok {
			continue
		}
		NoticeChatMessage(peer, "SERVER", ">", msg)
		// NoticeEntryUserCount(peer, p.Room.Id, aeug, titans)
	}

}

func (a *App) OnEnterRoom(p *AppPeer, roomId, _, _ uint16) bool {
	r, ok := p.Lobby.Rooms[roomId]
	if !ok {
		glog.Errorln("Not found")
		return false
	}
	if r.Status == model.RoomStateFull {
		return false
	}

	for _, u := range r.Users {
		peer, ok := a.users[u.UserId]
		if !ok {
			continue
		}
		NoticeJoinRoom(peer, p.UserId, p.Name, p.Team)
	}

	r.Enter(&p.User)
	p.Room = r

	if p.Lobby != nil {
		for id := range p.Lobby.Users {
			peer, ok := a.users[id]
			if !ok {
				continue
			}
			NoticeRoomStatus(peer, r.Id, r.Status)
			NoticeRoomUserCount(peer, r.Id, uint16(len(r.Users)))
			NoticeRoomJoinInfo(peer, r.Id, r.MaxPlayer)
		}
	}
	return true
}

func (a *App) OnExitRoom(p *AppPeer) {
	if p.Room == nil {
		return
	}
	r := p.Room

	if r.Owner == p.UserId {
		for _, u := range r.Users {
			if r.Owner != u.UserId {
				peer, ok := a.users[u.UserId]
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
		r.Exit(p.UserId)
		for _, u := range r.Users {
			peer, ok := a.users[u.UserId]
			if !ok {
				continue
			}
			NoticeExitRoom(peer, p.UserId, p.Name, p.Team)
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
			NoticeRoomName(peer, r.Id, r.Name)
			NoticeRoomStatus(peer, r.Id, r.Status)
			NoticeRoomUserCount(peer, r.Id, uint16(len(r.Users)))
			NoticeRoomJoinInfo(peer, r.Id, r.MaxPlayer)
		}
	}

}

func (a *App) OnGetRoomMatchEntryUserCount(p *AppPeer, roomId uint16) (aeug uint16, titans uint16) {
	if p.Lobby != nil {
		if room, ok := p.Lobby.Rooms[roomId]; ok {
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
	ok := a.startBattle(p.Room.LobbyId, active, p.Room.Rule)
	if ok {
		for _, u := range inactive {
			peer, ok := a.users[u.UserId]
			if ok {
				NoticeRemoveRoom(peer)
			}
		}
	} else {
		glog.Errorln("Failed to start battle")
	}
}
