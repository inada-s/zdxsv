package lobby

import (
	"slices"
	"strings"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"

	"github.com/golang/glog"
)

func lobbySetting(platform string, lobbyID uint16) *db.LobbySetting {
	s, err := db.DefaultDB.GetLobbySetting(platform, int(lobbyID))
	if err != nil {
		glog.Warningln("GetLobbySetting failure", platform, lobbyID, err)
	}
	if s == nil {
		return &db.LobbySetting{Platform: platform, No: int(lobbyID)}
	}
	return s
}

// lobbyBattleRule is the m_rule of a battle started in a lobby; nil for the default rule.
func lobbyBattleRule(s *db.LobbySetting) *model.Rule {
	if s.RuleID == "" {
		return nil
	}
	r, err := db.DefaultDB.GetRule(s.RuleID)
	if err != nil || r == nil {
		glog.Warningln("m_lobby_setting rule_id", s.Platform, s.No, s.RuleID, r, err)
		return nil
	}
	rule := model.NewRule()
	for k, v := range r.Values() {
		if !rule.SetByKey(k, *v) {
			glog.Warningln("m_rule", r.ID, k, *v, "is out of range")
		}
	}
	return rule
}

func lobbyCanStart(l *model.Lobby, s *db.LobbySetting) bool {
	l.Start = s.Start
	return l.CanBattleStart()
}

func lobbyHasHack(s *db.LobbySetting, hack string) bool {
	return slices.Contains(strings.Split(s.Hack, ","), hack)
}

func platformHasWideScreen(platform string) bool {
	for i := 0; i < model.LobbyCount; i++ {
		if lobbyHasHack(lobbySetting(platform, uint16(i)), model.HackWideScreen) {
			return true
		}
	}
	return false
}
