package lobby

import (
	"slices"
	"strings"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

func lobbySetting(platform string, lobbyID uint16) *db.LobbySetting {
	s, err := db.DefaultDB.GetLobbySetting(platform, int(lobbyID))
	if err != nil {
		zlog.Warn("GetLobbySetting failure", zap.Any("platform", platform), zap.Any("lobby_id", lobbyID), zap.Error(err))
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
		zlog.Warn("m_lobby_setting rule_id", zap.Any("platform", s.Platform), zap.Any("no", s.No), zap.Any("rule_id", s.RuleID), zap.Any("rule", r), zap.Error(err))
		return nil
	}
	rule := model.NewRule()
	for k, v := range r.Values() {
		if !rule.SetByKey(k, *v) {
			zlog.Warn("m_rule value out of range", zap.Any("rule_id", r.ID), zap.Any("key", k), zap.Any("value", *v))
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
