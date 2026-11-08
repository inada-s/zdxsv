package lobby

import (
	"zdxsv/pkg/config"
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

// lobbyBattleRule is the rule of a battle started in a lobby: the config's values, then the
// lobby setting's; nil (the default rule) when neither sets one.
func lobbyBattleRule(s *db.LobbySetting) *model.Rule {
	rule := model.NewRule()
	set := false
	c := config.Conf.Lobby
	for k, v := range map[string]uint16{"battle_gauge": c.BattleGauge, "battle_time": c.BattleTime, "battle_count": c.BattleCount} {
		if v != 0 && rule.SetByKey(k, v) {
			set = true
		}
	}
	values, err := s.RuleValues()
	if err != nil {
		glog.Warningln("m_lobby_setting rule", s.Platform, s.No, err)
	}
	for k, v := range values {
		if rule.SetByKey(k, v) {
			set = true
		}
	}
	if !set {
		return nil
	}
	return rule
}

func lobbyCanStart(l *model.Lobby, s *db.LobbySetting) bool {
	l.Start = s.Start
	return l.CanBattleStart()
}
