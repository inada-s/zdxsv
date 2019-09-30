package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/golang/glog"
	"github.com/valyala/gorpc"

	"zdxsv/pkg/assets"
	. "zdxsv/pkg/lobby/lobbyrpc"
)

var (
	current       statusParam
	tplStatus     *template.Template
	nicoLiveComms = map[string]interface{}{
		"co281463": true,
	}
)

const (
	timeFormat     = "2006年 01月02日 15:04"
	discordGuildId = "142729493566586880"
	discordUserId  = "147446105364234242"
	chatUrl        = "https://discordapp.com/channels/142729493566586880/142729493566586880"
	chatInviteUrl  = "https://discord.gg/0nQecfIg4HKnx258"
)

func init() {
	var err error
	var bin []byte

	bin, err = assets.Asset("assets/status.tpl")
	if err != nil {
		glog.Fatalln(err)
	}
	tplStatus, err = template.New("status").Parse(string(bin))
	if err != nil {
		glog.Fatalln(err)
	}

	current = statusParam{
		NowDate:         time.Now().Format(timeFormat),
		LobbyUserCount:  0,
		LobbyUsers:      []statusUser{},
		BattleUserCount: 0,
		BattleUsers:     []statusUser{},
		ChatUrl:         chatUrl,
		ChatInviteUrl:   chatInviteUrl,
	}
}

type statusUser struct {
	UserId string
	Name   string
	Team   string
	UDP    string
}

type statusChatUser struct {
	discordgo.User
	VoiceChat bool
	Online    bool
}

type statusLive struct {
	liveInfo
}

type statusParam struct {
	sync.RWMutex

	NowDate        string
	LobbyUserCount int
	LobbyUsers     []statusUser

	BattleUserCount int
	BattleUsers     []statusUser

	OnlineChatUsers  []statusChatUser
	OfflineChatUsers []statusChatUser

	ChatUrl       string
	ChatInviteUrl string

	Lives []statusLive
}

func pollLobby() {
	c := gorpc.NewTCPClient(conf.Lobby.RPCAddr)
	c.Start()
	defer c.Stop()
	for {
		time.Sleep(10 * time.Second)
		rawResp, err := c.CallTimeout(&StatusRequest{}, time.Second)
		if err != nil {
			glog.Errorln(err)
			continue
		}

		if res, ok := rawResp.(*StatusResponse); ok {
			current.Lock()
			current.NowDate = time.Now().Format(timeFormat)
			current.LobbyUsers = current.LobbyUsers[:0]
			checked := map[string]bool{}

			for _, u := range res.LobbyUsers {
				_, ok := checked[u.UserId]
				if ok {
					continue
				}
				checked[u.UserId] = true
				user := statusUser{
					UserId: u.UserId,
					Name:   u.Name,
					Team:   u.Team,
				}
				if u.UDP {
					user.UDP = fmt.Sprintf("○")
				}
				current.LobbyUsers = append(current.LobbyUsers, user)
			}

			current.BattleUsers = current.BattleUsers[:0]
			for _, b := range res.Battles {
				for _, u := range b.Users {
					_, ok := checked[u.UserId]
					if ok {
						continue
					}
					checked[u.UserId] = true
					user := statusUser{
						UserId: u.UserId,
						Name:   u.Name,
						Team:   u.Team,
					}
					if u.UDP {
						user.UDP = fmt.Sprintf("○")
					}
					current.BattleUsers = append(current.BattleUsers, user)
				}
			}
			current.LobbyUserCount = len(current.LobbyUsers)
			current.BattleUserCount = len(current.BattleUsers)
			current.Unlock()
		}
	}
}

func pollDiscordInternal() {
	updateCurrent := func(users map[string]*statusChatUser) {
		current.Lock()
		current.OnlineChatUsers = current.OnlineChatUsers[:0]
		current.OfflineChatUsers = current.OfflineChatUsers[:0]
		for id, user := range users {
			if id == discordUserId {
				continue
			}
			if user.Online {
				current.OnlineChatUsers = append(current.OnlineChatUsers, *user)
			} else {
				current.OfflineChatUsers = append(current.OfflineChatUsers, *user)
			}
		}
		current.Unlock()
	}

	var mUsers sync.Mutex
	users := map[string]*statusChatUser{}


	dg := discordgo.Session{}

	dg.OnReady = func(_ *discordgo.Session, m *discordgo.Ready) {
		mUsers.Lock()
		defer mUsers.Unlock()
		users = map[string]*statusChatUser{}

		var guild *discordgo.Guild
		for _, g := range m.Guilds {
			if g.ID == discordGuildId {
				guild = g
			}
		}
		if guild == nil {
			return
		}
		for _, mem := range guild.Members {
			users[mem.User.ID] = &statusChatUser{
				User: *mem.User,
			}
		}
		for _, mem := range guild.Presences {
			if mem.Status == "online" {
				if u, ok := users[mem.User.ID]; ok {
					u.Online = true
				}
			}
		}
		for _, mem := range guild.VoiceStates {
			if u, ok := users[mem.UserID]; ok {
				u.VoiceChat = true
			}
		}
		updateCurrent(users)
	}

	dg.OnGuildMemberAdd = func(_ *discordgo.Session, m *discordgo.Member) {
		if m.GuildID != discordGuildId {
			return
		}
		mUsers.Lock()
		defer mUsers.Unlock()
		if _, ok := users[m.User.ID]; ok {
			return
		}
		users[m.User.ID] = &statusChatUser{
			User: *m.User,
		}
		updateCurrent(users)
	}

	dg.OnGuildMemberRemove = func(_ *discordgo.Session, m *discordgo.Member) {
		if m.GuildID != discordGuildId {
			return
		}
		mUsers.Lock()
		defer mUsers.Unlock()
		delete(users, m.User.ID)
		updateCurrent(users)
	}

	dg.OnGuildMemberDelete = func(_ *discordgo.Session, m *discordgo.Member) {
		if m.GuildID != discordGuildId {
			return
		}
		mUsers.Lock()
		defer mUsers.Unlock()
		delete(users, m.User.ID)
		updateCurrent(users)
	}

	dg.OnGuildMemberUpdate = func(_ *discordgo.Session, m *discordgo.Member) {
		if m.GuildID != discordGuildId {
			return
		}
		mUsers.Lock()
		defer mUsers.Unlock()
		if _, ok := users[m.User.ID]; !ok {
			return
		}
		users[m.User.ID].User = *m.User
		updateCurrent(users)
	}

	dg.OnVoiceStateUpdate = func(_ *discordgo.Session, m *discordgo.VoiceState) {
		mUsers.Lock()
		defer mUsers.Unlock()
		if _, ok := users[m.UserID]; !ok {
			return
		}
		users[m.UserID].VoiceChat = m.ChannelID != ""
		updateCurrent(users)
	}

	dg.OnUserUpdate = func(_ *discordgo.Session, m *discordgo.User) {
		mUsers.Lock()
		defer mUsers.Unlock()
		if _, ok := users[m.ID]; !ok {
			return
		}
		users[m.ID].User = *m
	}

	dg.OnPresenceUpdate = func(_ *discordgo.Session, m *discordgo.PresenceUpdate) {
		mUsers.Lock()
		defer mUsers.Unlock()
		if _, ok := users[m.User.ID]; !ok {
			return
		}
		if m.User.Avatar != "" {
			users[m.User.ID].Avatar = m.User.Avatar
		}
		if m.User.Username != "" {
			users[m.User.ID].Username = m.User.Username
		}
		users[m.User.ID].Online = m.Status == "online"
		updateCurrent(users)
	}

        var wg sync.WaitGroup
	var once = new(sync.Once)
        wg.Add(1)
	dg.OnDisconnect = func(_ *discordgo.Session) {
		glog.Errorln("OnDisconnect()")
		once.Do(wg.Done)
	}

	if err := dg.Login("mecha.is06+zdxsv@gmail.com", "zdxserver"); err != nil {
		glog.Errorln(err)
		return
	}

	if err := dg.Open(); err != nil {
		glog.Errorln(err)
		return
	}
	defer dg.Close()
	dg.UpdateStatus(1, "調整中")
        wg.Wait()
}

type liveInfo struct {
	LiveId        string
	LiveUrl       string
	Status        string `xml:"status,attr"`
	Title         string `xml:"streaminfo>title"`
	Description   string `xml:"streaminfo>description"`
	ProviderType  string `xml:"streaminfo>provider_type"`
	ThumbUrl      string `xml:"communityinfo>thumbnail"`
	CommunityName string `xml:"communityinfo>name"`
}

func getNicoLiveInfo(liveId string) (*liveInfo, error) {
	resp, err := http.Get(fmt.Sprintf("http://live.nicovideo.jp/api/getstreaminfo/lv%s", liveId))
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	live := liveInfo{}
	err = xml.Unmarshal(body, &live)
	if err != nil {
		return nil, err
	}
	live.LiveUrl = fmt.Sprintf("http://live.nicovideo.jp/watch/lv%s", liveId)
	live.LiveId = liveId
	return &live, nil
}

func liveUpdateLoopInternal() {
	cookieJar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: cookieJar,
	}
	loginResp, err := client.PostForm("https://secure.nicovideo.jp/secure/login", url.Values{
		"mail":     []string{"zdxserver@gmail.com"},
		"password": []string{"D5zSf7zr"},
	})
	if err != nil {
		glog.Errorln(err)
		return
	}
	loginResp.Body.Close()

	playerStatus := struct {
		Time         int64  `xml:"time,attr"`
		Status       string `xml:"status,attr"`
		Title        string `xml:"stream>title"`
		Description  string `xml:"stream>description"`
		WatchCount   string `xml:"stream>watch_count"`
		CommentCount string `xml:"stream>comment_count"`
		OpenTime     int64  `xml:"stream>start_time"`
		EndTime      int64  `xml:"stream>end_time"`
	}{}

	for {
		current.RLock()
		lives := make([]statusLive, 0, len(current.Lives))
		lives = append(lives, current.Lives...)
		current.RUnlock()

		if 0 < len(lives) {
			for i := 0; i < len(lives); i++ {
				resp, err := client.Get(fmt.Sprintf("http://live.nicovideo.jp/api/getplayerstatus?v=lv%s", lives[i].LiveId))
				if err != nil {
					glog.Errorln("getplayerstatus request failed.", err)
					lives[i].Status = "err"
					return
				}
				decoder := xml.NewDecoder(resp.Body)
				err = decoder.Decode(&playerStatus)
				resp.Body.Close()
				if err != nil {
					glog.Errorln("getplayerstatus decode failed", err)
					lives[i].Status = "err"
					return
				}
				lives[i].Status = playerStatus.Status
				if playerStatus.Status == "ok" {
					if playerStatus.EndTime < playerStatus.Time {
						lives[i].Status = "end" // need?
					}
				}
			}

			current.Lock()
			for i := len(current.Lives) - 1; i >= 0; i-- {
				for _, live := range lives {
					if live.LiveId == current.Lives[i].LiveId {
						if live.Status != "ok" {
							current.Lives = append(current.Lives[:i], current.Lives[i+1:]...)
						}
						break
					}
				}
			}
			current.Unlock()
		}
		time.Sleep(time.Minute)
	}
}

func pollNiconicoInternal() {
	resp, err := http.Get("http://live.nicovideo.jp/api/getalertinfo")
	if err != nil {
		glog.Errorln("getalertinfo failed. ", err)
		return
	}
	defer resp.Body.Close()

	alInfo := struct {
		UserId   string `xml:"user_id"`
		UserHash string `xml:"user_hash"`
		Addr     string `xml:"ms>addr"`
		Port     string `xml:"ms>port"`
		Thread   string `xml:"ms>thread"`
	}{}

	decoder := xml.NewDecoder(resp.Body)
	err = decoder.Decode(&alInfo)
	if err != nil {
		glog.Errorln("getalertinfo failed. ", err)
		return
	}

	glog.Infoln("AlertInfo", alInfo)
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", alInfo.Addr, alInfo.Port))
	if err != nil {
		glog.Errorln("getalertinfo failed. ", err)
		return
	}

	_, err = conn.Write([]byte(fmt.Sprintf(`<thread thread="%s" version="20061206" res_from="-1"/>`+"\x00", alInfo.Thread)))
	if err != nil {
		glog.Errorln("first write failed. ", err)
		return
	}

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString(byte(0))
		if err != nil {
			glog.Errorln(err)
			return
		}
		elems := strings.Split(line, ">")
		if len(elems) < 2 {
			continue
		}
		elems = strings.Split(elems[1], "<")
		if len(elems) < 2 {
			continue
		}
		elems = strings.Split(elems[0], ",")
		if len(elems) < 3 {
			continue
		}
		liveId := elems[0]
		comId := elems[1]
		if _, ok := nicoLiveComms[comId]; !ok {
			continue
		}
		info, err := getNicoLiveInfo(liveId)
		if err != nil {
			glog.Errorln(err)
			continue
		}
		glog.Infoln(info)
		if info.Status != "ok" {
			continue
		}
		if info.ProviderType != "community" {
			continue
		}

		current.Lock()
		exist := false
		for _, live := range current.Lives {
			if live.LiveId == info.LiveId {
				exist = true
				break
			}
		}
		if !exist {
			current.Lives = append(current.Lives, statusLive{*info})
		}
		current.Unlock()
	}
}

func pollDiscord() {
	for {
		pollDiscordInternal()
		time.Sleep(time.Minute)
	}
}

func liveUpdateLoop() {
	for {
		liveUpdateLoopInternal()
		time.Sleep(time.Minute)
	}
}

func pollNiconico() {
	for {
		pollNiconicoInternal()
		time.Sleep(time.Minute)
	}
}

func redirectToIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}

func getIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf8")
	w.WriteHeader(200)
	current.RLock()
	defer current.RUnlock()
	err := tplStatus.Execute(w, current)
	if err != nil {
		glog.Errorln(err)
	}
}

func getApiStat(w http.ResponseWriter, r *http.Request) {
	current.RLock()
	defer current.RUnlock()
	bin, err := json.Marshal(current)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf8")
	w.WriteHeader(200)
	w.Write(bin)
}

func dummyCurrent() {
	current.Lock()
	defer current.Unlock()
	current.LobbyUsers = current.LobbyUsers[:0]
	current.BattleUsers= current.BattleUsers[:0]

	for i := 0; i < 10; i++ {
		user := statusUser{
			UserId: fmt.Sprintf("%06d", i),
			Name:   fmt.Sprintf("%06dさん", i),
			Team:   fmt.Sprintf("%06dチーム", i),
		}
		if i%2 == 0 {
			user.UDP = fmt.Sprintf("○")
		}
		current.LobbyUsers = append(current.LobbyUsers, user)
		current.BattleUsers= append(current.BattleUsers, user)
	}
	go func() {
		for{
		time.Sleep(time.Second)
		current.Lock()
		current.NowDate = time.Now().Format(timeFormat)
		current.Unlock()
	}
	}()
}

func mainStatus() {
	go pollLobby()
	go pollDiscord()
	go pollNiconico()
	go liveUpdateLoop()


	if _, err := assets.Asset("assets/checkfile"); err != nil {
		glog.Fatalln(err)
	}
	router := http.NewServeMux()
	router.HandleFunc("/", getIndex)
	router.HandleFunc("/status", redirectToIndex)
	router.HandleFunc("/api/stat", getApiStat)
	router.HandleFunc("/assets/", handleAssets)
	err := http.ListenAndServe(conf.Status.Addr, router)
	if err != nil {
		glog.Fatalln(err)
	}
}
