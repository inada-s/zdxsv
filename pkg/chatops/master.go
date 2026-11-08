package chatops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"

	"github.com/blang/semver"
)

// BanTable is the spreadsheet table that becomes m_ban.
const BanTable = "m_ban"

// ServerSettingTable is the spreadsheet table that becomes m_server_setting, when the sheet has it.
const ServerSettingTable = "m_server_setting"

// MasterUpdater replaces the master data with the spreadsheet's tables, only when every table
// is valid. Tables zdxsv does not use are ignored.
type MasterUpdater struct {
	Sheet SheetReader
	// DB is db.DefaultDB when nil.
	DB db.DB
}

// MasterCommands are /op master (the spreadsheet's link) and /op master-up (Update).
func MasterCommands(s *Spreadsheet, u *MasterUpdater) map[string]func(context.Context) (string, error) {
	return map[string]func(context.Context) (string, error){
		"op master": func(context.Context) (string, error) {
			return "https://docs.google.com/spreadsheets/d/" + s.ID, nil
		},
		"op master-up": u.Update,
	}
}

func (u *MasterUpdater) Update(ctx context.Context) (string, error) {
	sheets, err := u.Sheet.ReadAll(ctx)
	if err != nil {
		return "", err
	}
	tables, err := ParseTables(sheets)
	if err != nil {
		return "", err
	}
	d := u.DB
	if d == nil {
		d = db.DefaultDB
	}
	m, err := ParseMaster(d, tables)
	if err != nil {
		return "", err
	}
	if err := d.ReplaceMaster(m); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s updated: %d rows", BanTable, len(m.Bans))
	if m.ServerSettings != nil {
		msg += fmt.Sprintf(", %s: %d rows", ServerSettingTable, len(m.ServerSettings))
	}
	if m.LobbySettings != nil {
		msg += fmt.Sprintf(", %s: %d rows", LobbySettingTable, len(m.LobbySettings))
	}
	return msg, nil
}

// ParseMaster checks every table zdxsv uses and returns them, or all the problems found.
func ParseMaster(d db.DB, tables map[string][][]string) (*db.Master, error) {
	m := &db.Master{}
	table, ok := tables[BanTable]
	if !ok {
		return nil, fmt.Errorf("no table @%s", BanTable)
	}
	bans, err := ParseBans(d, table)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", BanTable, err)
	}
	m.Bans = bans
	if table, ok := tables[ServerSettingTable]; ok {
		if m.ServerSettings, err = ParseServerSettings(table); err != nil {
			return nil, fmt.Errorf("%s: %w", ServerSettingTable, err)
		}
	}
	if table, ok := tables[LobbySettingTable]; ok {
		if m.LobbySettings, err = ParseLobbySettings(table); err != nil {
			return nil, fmt.Errorf("%s: %w", LobbySettingTable, err)
		}
		if m.LobbySettings == nil {
			m.LobbySettings = []*db.LobbySetting{}
		}
	}
	return m, nil
}

// ParseServerSettings checks the m_server_setting table (header first): columns key and value.
// min_pcsx2_version must be empty or a version (zdxsv-X.Y.Z).
func ParseServerSettings(table [][]string) (map[string]string, error) {
	col, errs := columns(table, []string{"key", "value"}, nil)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	settings := map[string]string{}
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		key, value := strings.TrimSpace(row[col["key"]]), strings.TrimSpace(row[col["value"]])
		if key == "" {
			errs = append(errs, fmt.Errorf("row %d: no key", line))
		}
		if prev, ok := seen[key]; ok {
			errs = append(errs, fmt.Errorf("row %d: key %q is also on row %d", line, key, prev))
		}
		seen[key] = line
		if key == "min_pcsx2_version" && value != "" {
			if _, err := semver.Parse(strings.TrimPrefix(value, "zdxsv-")); err != nil {
				errs = append(errs, fmt.Errorf("row %d: min_pcsx2_version %q is not a version like zdxsv-1.2.3", line, value))
			}
		}
		settings[key] = value
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return settings, nil
}

// ParseBans checks the m_ban table (header first). Columns: key (a user id, which must exist)
// and until are required, reason and created optional. A time without an offset is UTC.
func ParseBans(d db.DB, table [][]string) ([]*db.MasterBan, error) {
	col, errs := columns(table, []string{"key", "until"}, []string{"reason", "created"})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	bans := []*db.MasterBan{}
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		cell := func(name string) string {
			if i, ok := col[name]; ok {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		b := &db.MasterBan{Key: cell("key"), Reason: cell("reason")}
		if _, err := d.GetUser(b.Key); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				errs = append(errs, fmt.Errorf("row %d: key %q is no user", line, b.Key))
			} else {
				errs = append(errs, fmt.Errorf("row %d: key %q: %w", line, b.Key, err))
			}
		}
		if prev, ok := seen[b.Key]; ok {
			errs = append(errs, fmt.Errorf("row %d: key %q is also on row %d", line, b.Key, prev))
		}
		seen[b.Key] = line
		var err error
		if b.Until, err = parseTime(cell("until")); err != nil {
			errs = append(errs, fmt.Errorf("row %d: until %q is not a time", line, cell("until")))
		}
		if s := cell("created"); s != "" {
			if b.Created, err = parseTime(s); err != nil {
				errs = append(errs, fmt.Errorf("row %d: created %q is not a time", line, s))
			}
		}
		bans = append(bans, b)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return bans, nil
}

func columns(table [][]string, required, optional []string) (map[string]int, []error) {
	if len(table) == 0 {
		return nil, []error{errors.New("no header")}
	}
	known := map[string]bool{}
	for _, name := range append(append([]string{}, required...), optional...) {
		known[name] = true
	}
	col := map[string]int{}
	var errs []error
	for i, name := range table[0] {
		if !known[name] {
			errs = append(errs, fmt.Errorf("unknown column %q", name))
			continue
		}
		if _, ok := col[name]; ok {
			errs = append(errs, fmt.Errorf("column %s appears twice", name))
		}
		col[name] = i
	}
	for _, name := range required {
		if _, ok := col[name]; !ok {
			errs = append(errs, fmt.Errorf("no column %s", name))
		}
	}
	return col, errs
}

var timeLayouts = []string{
	time.RFC3339,
	"2006-1-2 15:04:05Z07:00",
	"2006-1-2 15:04:05",
	"2006-1-2 15:04",
	"2006-1-2",
}

func parseTime(s string) (time.Time, error) {
	s = strings.ReplaceAll(s, "/", "-")
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

// LobbySettingTable is the spreadsheet table that becomes m_lobby_setting, when the sheet has it.
const LobbySettingTable = "m_lobby_setting"

var lobbyStarts = []string{model.Start2v2, model.Start1v1, model.StartSolo}

// ParseLobbySettings checks the m_lobby_setting table (header first): platform (console or emu)
// and no (lobby number) are required; start, explain and the model.RuleKeys columns optional.
// An empty cell keeps the lobby's default.
func ParseLobbySettings(table [][]string) ([]*db.LobbySetting, error) {
	var ruleKeys []string
	for k := range model.RuleKeys {
		ruleKeys = append(ruleKeys, k)
	}
	sort.Strings(ruleKeys)
	col, errs := columns(table, []string{"platform", "no"}, append([]string{"start", "explain"}, ruleKeys...))
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	var settings []*db.LobbySetting
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		cell := func(name string) string {
			i, ok := col[name]
			if !ok || len(row) <= i {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		s := &db.LobbySetting{Platform: cell("platform"), Start: cell("start"), Explain: cell("explain")}
		if s.Platform != model.PlatformConsole && s.Platform != model.PlatformEmu {
			errs = append(errs, fmt.Errorf("row %d: platform %q is not %s or %s", line, s.Platform, model.PlatformConsole, model.PlatformEmu))
		}
		no, err := strconv.Atoi(cell("no"))
		if err != nil || no < 0 || model.LobbyCount <= no {
			errs = append(errs, fmt.Errorf("row %d: no %q is not a lobby number 0-%d", line, cell("no"), model.LobbyCount-1))
		}
		s.No = no
		key := fmt.Sprintf("%s %d", s.Platform, no)
		if prev, ok := seen[key]; ok {
			errs = append(errs, fmt.Errorf("row %d: lobby %s is also on row %d", line, key, prev))
		}
		seen[key] = line
		if s.Start != "" && !slices.Contains(lobbyStarts, s.Start) {
			errs = append(errs, fmt.Errorf("row %d: start %q is not one of %v", line, s.Start, lobbyStarts))
		}
		rule := map[string]uint16{}
		for _, k := range ruleKeys {
			v := cell(k)
			if v == "" {
				continue
			}
			n, err := strconv.ParseUint(v, 10, 16)
			if err != nil || !model.NewRule().SetByKey(k, uint16(n)) {
				errs = append(errs, fmt.Errorf("row %d: %s %q is not one of %v", line, k, v, model.RuleValues(k)))
				continue
			}
			rule[k] = uint16(n)
		}
		if len(rule) > 0 {
			b, _ := json.Marshal(rule)
			s.Rule = string(b)
		}
		settings = append(settings, s)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return settings, nil
}
