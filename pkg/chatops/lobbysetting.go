package chatops

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"
)

// LobbySettingTable is the spreadsheet table that becomes m_lobby_setting.
const LobbySettingTable = "m_lobby_setting"

// ParseLobbySettings checks every row of the m_lobby_setting table (header first) and returns
// them, or all the problems found. Columns: platform (console, emu) and lobby_id are required;
// battle_gauge, battle_time, battle_count are values of the rule menu, empty or 0 for the default.
func ParseLobbySettings(table [][]string) ([]*db.LobbySetting, error) {
	if len(table) == 0 {
		return nil, errors.New("no header")
	}
	col := map[string]int{}
	var errs []error
	for i, name := range table[0] {
		switch name {
		case "platform", "lobby_id", "battle_gauge", "battle_time", "battle_count":
		default:
			errs = append(errs, fmt.Errorf("unknown column %q", name))
			continue
		}
		if _, ok := col[name]; ok {
			errs = append(errs, fmt.Errorf("column %s appears twice", name))
		}
		col[name] = i
	}
	for _, name := range []string{"platform", "lobby_id"} {
		if _, ok := col[name]; !ok {
			errs = append(errs, fmt.Errorf("no column %s", name))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	settings := []*db.LobbySetting{}
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		cell := func(name string) string {
			if i, ok := col[name]; ok {
				return row[i]
			}
			return ""
		}
		num := func(name string, required bool, valid func(uint16) bool) uint16 {
			s := cell(name)
			if s == "" && !required {
				return 0
			}
			v, err := strconv.ParseUint(s, 10, 16)
			if err != nil || (valid != nil && v != 0 && !valid(uint16(v))) {
				errs = append(errs, fmt.Errorf("row %d: %s %q is invalid", line, name, s))
			}
			return uint16(v)
		}
		s := &db.LobbySetting{
			Platform:    cell("platform"),
			LobbyID:     num("lobby_id", true, nil),
			BattleGauge: num("battle_gauge", false, model.ValidBattleGauge),
			BattleTime:  num("battle_time", false, model.ValidBattleTime),
			BattleCount: num("battle_count", false, model.ValidBattleCount),
		}
		if s.LobbyID >= model.LobbyCount {
			errs = append(errs, fmt.Errorf("row %d: lobby_id %d is not below %d", line, s.LobbyID, model.LobbyCount))
		}
		if s.Platform != model.PlatformConsole && s.Platform != model.PlatformEmu {
			errs = append(errs, fmt.Errorf("row %d: platform %q is not %s or %s", line, s.Platform, model.PlatformConsole, model.PlatformEmu))
		}
		key := fmt.Sprint(s.Platform, "/", s.LobbyID)
		if prev, ok := seen[key]; ok {
			errs = append(errs, fmt.Errorf("row %d: %s lobby %d is also on row %d", line, s.Platform, s.LobbyID, prev))
		}
		seen[key] = line
		settings = append(settings, s)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return settings, nil
}

// LobbySettingUpdater replaces m_lobby_setting with the spreadsheet's table, only when every
// row is valid.
type LobbySettingUpdater struct {
	Sheet SheetReader
	// DB is db.DefaultDB when nil.
	DB db.DB
}

func (u *LobbySettingUpdater) Update(ctx context.Context) (string, error) {
	sheets, err := u.Sheet.ReadAll(ctx)
	if err != nil {
		return "", err
	}
	tables, err := ParseTables(sheets)
	if err != nil {
		return "", err
	}
	table, ok := tables[LobbySettingTable]
	if !ok {
		return "", fmt.Errorf("no table @%s", LobbySettingTable)
	}
	settings, err := ParseLobbySettings(table)
	if err != nil {
		return "", err
	}
	d := u.DB
	if d == nil {
		d = db.DefaultDB
	}
	if err := d.ReplaceLobbySettings(settings); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s updated: %d rows", LobbySettingTable, len(settings)), nil
}
