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

// lobbyBattleRule is the rule of a battle started in a lobby: its m_rule, else the default
// rule (the config's values; nil when it sets none).
func lobbyBattleRule(s *db.LobbySetting) *model.Rule {
	if s.RuleID != "" {
		r, err := db.DefaultDB.GetRule(s.RuleID)
		if err != nil || r == nil {
			glog.Warningln("m_lobby_setting rule_id", s.Platform, s.No, s.RuleID, r, err)
		} else {
			rule := model.NewRule()
			for k, v := range r.Values() {
				if !rule.SetByKey(k, *v) {
					glog.Warningln("m_rule", r.ID, k, *v, "is not in the menu")
				}
			}
			return rule
		}
	}
	rule := model.NewRule()
	set := false
	c := config.Conf.Lobby
	for k, v := range map[string]uint16{"battle_gauge": c.BattleGauge, "battle_time": c.BattleTime, "battle_count": c.BattleCount} {
		if v != 0 && rule.SetByKey(k, v) {
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

func lobbyHack(s *db.LobbySetting) string {
	if s.Hack != "" {
		return s.Hack
	}
	switch s.No {
	case 3:
		return model.HackPadDelay
	case 4:
		return model.HackWideScreen
	}
	return model.HackNone
}
