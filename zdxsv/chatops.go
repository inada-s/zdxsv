// Chatops updates the master data in the db from a Google Spreadsheet on a Discord
// slash command.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
	"go.uber.org/zap"
)

// SheetReader returns the cells of every sheet of a spreadsheet as text, rows of cells.
type SheetReader interface {
	ReadAll(ctx context.Context) ([][][]string, error)
}

// Spreadsheet reads a Google Spreadsheet through the Sheets API v4.
type Spreadsheet struct {
	ID string
	// Client authorizes the requests (scope spreadsheets.readonly).
	Client *http.Client
	// BaseURL replaces https://sheets.googleapis.com, for tests.
	BaseURL string
}

const sheetRange = "A1:Z1000"

func (s *Spreadsheet) ReadAll(ctx context.Context) ([][][]string, error) {
	base := s.BaseURL
	if base == "" {
		base = "https://sheets.googleapis.com"
	}
	base += "/v4/spreadsheets/" + url.PathEscape(s.ID)

	var meta struct {
		Sheets []struct {
			Properties struct {
				Title string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := s.get(ctx, base+"?fields=sheets.properties.title", &meta); err != nil {
		return nil, err
	}
	if len(meta.Sheets) == 0 {
		return nil, nil
	}

	q := url.Values{}
	for _, sh := range meta.Sheets {
		q.Add("ranges", "'"+strings.ReplaceAll(sh.Properties.Title, "'", "''")+"'!"+sheetRange)
	}
	var values struct {
		ValueRanges []struct {
			Values [][]string `json:"values"`
		} `json:"valueRanges"`
	}
	if err := s.get(ctx, base+"/values:batchGet?"+q.Encode(), &values); err != nil {
		return nil, err
	}
	ret := make([][][]string, 0, len(values.ValueRanges))
	for _, vr := range values.ValueRanges {
		ret = append(ret, vr.Values)
	}
	return ret, nil
}

func (s *Spreadsheet) get(ctx context.Context, u string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("sheets api: %s: %.200s", res.Status, body)
	}
	return json.Unmarshal(body, v)
}

// ParseTables finds the tables in the sheets. A table starts at a cell "@<name>" (name starting
// with "m_"), its header row is the row below from that column up to the first empty cell, and
// its data rows follow up to the first row empty in those columns. The header row is the first
// row of each table; short rows are padded with "".
func ParseTables(sheets [][][]string) (map[string][][]string, error) {
	tables := map[string][][]string{}
	for _, rows := range sheets {
		for i := 0; i < len(rows); i++ {
			left := -1
			for j, c := range rows[i] {
				if strings.HasPrefix(c, "@m_") {
					left = j
				}
			}
			if left < 0 || i+1 >= len(rows) {
				continue
			}
			name := strings.TrimSpace(rows[i][left][1:])
			if _, ok := tables[name]; ok {
				return nil, fmt.Errorf("table %s appears twice", name)
			}
			right := left
			for right < len(rows[i+1]) && rows[i+1][right] != "" {
				right++
			}
			if right == left {
				return nil, fmt.Errorf("table %s has no header", name)
			}
			var table [][]string
			for i = i + 1; i < len(rows); i++ {
				r := make([]string, right-left)
				empty := true
				for j := left; j < right && j < len(rows[i]); j++ {
					r[j-left] = strings.TrimSpace(rows[i][j])
					if r[j-left] != "" {
						empty = false
					}
				}
				if empty {
					break
				}
				table = append(table, r)
			}
			tables[name] = table
		}
	}
	return tables, nil
}

// BanTable is the spreadsheet table that becomes m_ban.
const BanTable = "m_ban"

// ServerSettingTable is the spreadsheet table that becomes m_server_setting, when the sheet has it.
const ServerSettingTable = "m_server_setting"

// MasterUpdater replaces the master data with the spreadsheet's tables, only when every table
// is valid. Tables zdxsv does not use are ignored.
type MasterUpdater struct {
	Sheet SheetReader
	// DB is db.DefaultDB when nil.
	DB DB
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
		d = DefaultDB
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
	if m.Rules != nil {
		msg += fmt.Sprintf(", %s: %d rows", RuleTable, len(m.Rules))
	}
	return msg, nil
}

// checkRuleIDs checks that each rule_id of m_lobby_setting is in m_rule: the sheet's when it has
// the table, else the DB's.
func checkRuleIDs(d DB, m *Master) error {
	if m.LobbySettings == nil && m.Rules == nil {
		return nil
	}
	settings := m.LobbySettings
	if settings == nil {
		var err error
		if settings, err = d.GetLobbySettings(); err != nil {
			return err
		}
	}
	var errs []error
	for _, s := range settings {
		if s.RuleID == "" {
			continue
		}
		found := false
		if m.Rules != nil {
			found = slices.ContainsFunc(m.Rules, func(r *MRule) bool { return r.ID == s.RuleID })
		} else {
			r, err := d.GetRule(s.RuleID)
			if err != nil {
				return err
			}
			found = r != nil
		}
		if !found {
			errs = append(errs, fmt.Errorf("lobby %s %d: rule_id %q is not in %s", s.Platform, s.No, s.RuleID, RuleTable))
		}
	}
	return errors.Join(errs...)
}

// ParseMaster checks every table zdxsv uses and returns them, or all the problems found.
func ParseMaster(d DB, tables map[string][][]string) (*Master, error) {
	m := &Master{}
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
	if table, ok := tables[RuleTable]; ok {
		if m.Rules, err = ParseRules(table); err != nil {
			return nil, fmt.Errorf("%s: %w", RuleTable, err)
		}
		if m.Rules == nil {
			m.Rules = []*MRule{}
		}
	}
	if table, ok := tables[LobbySettingTable]; ok {
		if m.LobbySettings, err = ParseLobbySettings(table); err != nil {
			return nil, fmt.Errorf("%s: %w", LobbySettingTable, err)
		}
		if m.LobbySettings == nil {
			m.LobbySettings = []*MLobbySetting{}
		}
	}
	if err := checkRuleIDs(d, m); err != nil {
		return nil, fmt.Errorf("%s: %w", LobbySettingTable, err)
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
func ParseBans(d DB, table [][]string) ([]*MasterBan, error) {
	col, errs := columns(table, []string{"key", "until"}, []string{"reason", "created"})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	bans := []*MasterBan{}
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		cell := func(name string) string {
			if i, ok := col[name]; ok {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		b := &MasterBan{Key: cell("key"), Reason: cell("reason")}
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
		if strings.HasPrefix(name, "#") {
			continue
		}
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

// RuleTable is the spreadsheet table that becomes m_rule, when the sheet has it.
const RuleTable = "m_rule"

var lobbyStarts = []string{Start2v2, Start1v1, StartSolo}

var lobbyHacks = []string{HackNone, HackPadDelay, HackWideScreen, HackZproxyTest, HackChatCommand}

// parseLobbyHack checks a comma separated hack cell and returns it without spaces.
func parseLobbyHack(cell string) (string, error) {
	if cell == "" {
		return "", nil
	}
	var hacks []string
	for _, h := range strings.Split(cell, ",") {
		h = strings.TrimSpace(h)
		if !slices.Contains(lobbyHacks, h) {
			return "", fmt.Errorf("hack %q is not one of %v", h, lobbyHacks)
		}
		if slices.Contains(hacks, h) {
			return "", fmt.Errorf("hack %q appears twice", h)
		}
		hacks = append(hacks, h)
	}
	if len(hacks) > 1 && slices.Contains(hacks, HackNone) {
		return "", fmt.Errorf("hack %s cannot be combined", HackNone)
	}
	if slices.Contains(hacks, HackPadDelay) && slices.Contains(hacks, HackWideScreen) {
		return "", fmt.Errorf("hack %s and %s cannot be combined", HackPadDelay, HackWideScreen)
	}
	return strings.Join(hacks, ","), nil
}

// ParseLobbySettings checks the m_lobby_setting table (header first): platform (console or emu)
// and no (lobby number) are required; start, explain, rule_id and hack optional.
// An empty cell keeps the lobby's default.
func ParseLobbySettings(table [][]string) ([]*MLobbySetting, error) {
	col, errs := columns(table, []string{"platform", "no"}, []string{"start", "explain", "rule_id", "hack"})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	var settings []*MLobbySetting
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		cell := cellFunc(col, row)
		s := &MLobbySetting{Platform: cell("platform"), Start: cell("start"), Explain: cell("explain"), RuleID: cell("rule_id"), Hack: cell("hack")}
		if s.Platform != PlatformConsole && s.Platform != PlatformEmu {
			errs = append(errs, fmt.Errorf("row %d: platform %q is not %s or %s", line, s.Platform, PlatformConsole, PlatformEmu))
		}
		no, err := strconv.Atoi(cell("no"))
		if err != nil || no < 0 || LobbyCount <= no {
			errs = append(errs, fmt.Errorf("row %d: no %q is not a lobby number 0-%d", line, cell("no"), LobbyCount-1))
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
		if s.Hack, err = parseLobbyHack(s.Hack); err != nil {
			errs = append(errs, fmt.Errorf("row %d: %w", line, err))
		}
		settings = append(settings, s)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return settings, nil
}

// ParseRules checks the m_rule table (header first): id is required, the model.RuleKeys
// columns optional; an empty cell is the menu default.
func ParseRules(table [][]string) ([]*MRule, error) {
	var ruleKeys []string
	for k := range RuleKeys {
		ruleKeys = append(ruleKeys, k)
	}
	sort.Strings(ruleKeys)
	col, errs := columns(table, []string{"id"}, ruleKeys)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	var rules []*MRule
	seen := map[string]int{}
	for n, row := range table[1:] {
		line := n + 1
		cell := cellFunc(col, row)
		r := &MRule{ID: cell("id")}
		if r.ID == "" {
			errs = append(errs, fmt.Errorf("row %d: no id", line))
		} else if prev, ok := seen[r.ID]; ok {
			errs = append(errs, fmt.Errorf("row %d: id %q is also on row %d", line, r.ID, prev))
		}
		seen[r.ID] = line
		values := r.Values()
		for _, k := range ruleKeys {
			*values[k] = RuleDefault(k)
			v := cell(k)
			if v == "" {
				continue
			}
			n, err := strconv.ParseUint(v, 10, 16)
			if err != nil || !NewRule().SetByKey(k, uint16(n)) {
				errs = append(errs, fmt.Errorf("row %d: %s %q is out of range", line, k, v))
				continue
			}
			*values[k] = uint16(n)
		}
		rules = append(rules, r)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return rules, nil
}

func cellFunc(col map[string]int, row []string) func(string) string {
	return func(name string) string {
		i, ok := col[name]
		if !ok || len(row) <= i {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
}

const (
	interactionPing               = 1
	interactionApplicationCommand = 2
	optionSubCommand              = 1

	responsePong                    = 1
	responseChannelMessage          = 4
	responseDeferredChannelMessage  = 5
	messageFlagEphemeral            = 64
	discordMessageMax               = 2000
	updateTimeout                   = 2 * time.Minute
	interactionBodyMax              = 1 << 20
	defaultDiscordAPIURL            = "https://discord.com/api/v10"
	discordSignatureHeader          = "X-Signature-Ed25519"
	discordSignatureTimestampHeader = "X-Signature-Timestamp"
)

// Discord serves a Discord application's interactions endpoint (POST). Each Commands entry is a
// slash command, "op master-up" for the subcommand master-up of /op: it answers at once with a deferred message, runs, and edits that message
// to the result. One command runs at a time.
type Discord struct {
	PublicKey ed25519.PublicKey
	// Allow holds the user and role ids that may run the commands; nobody when empty.
	Allow    map[string]bool
	Commands map[string]func(ctx context.Context) (string, error)
	// APIURL replaces https://discord.com/api/v10, for tests.
	APIURL string
	Client *http.Client

	running sync.Mutex
	wg      sync.WaitGroup
}

type interaction struct {
	Type          int    `json:"type"`
	ApplicationID string `json:"application_id"`
	Token         string `json:"token"`
	Data          struct {
		Name    string `json:"name"`
		Options []struct {
			Name string `json:"name"`
			Type int    `json:"type"`
		} `json:"options"`
	} `json:"data"`
	Member *struct {
		User  struct{ ID string } `json:"user"`
		Roles []string            `json:"roles"`
	} `json:"member"`
	User *struct{ ID string } `json:"user"`
}

func (d *Discord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, interactionBodyMax))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	sig, err := hex.DecodeString(r.Header.Get(discordSignatureHeader))
	ts := r.Header.Get(discordSignatureTimestampHeader)
	if err != nil || len(d.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(d.PublicKey, append([]byte(ts), body...), sig) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}
	var in interaction
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	switch in.Type {
	case interactionPing:
		writeJSON(w, map[string]int{"type": responsePong})
		return
	case interactionApplicationCommand:
	default:
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	name := in.commandName()
	run, ok := d.Commands[name]
	if !ok {
		reply(w, "unknown command "+name)
		return
	}
	if !d.allowed(&in) {
		reply(w, "not allowed")
		return
	}
	if !d.running.TryLock() {
		reply(w, "another command is running")
		return
	}
	logger.Info("discord command", zap.Any("name", name), zap.Any("user_id", in.userID()))
	answered := make(chan struct{})
	defer close(answered)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.running.Unlock()
		// Discord rejects the edit of a message whose deferred answer it has not got yet.
		<-answered
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		defer cancel()
		msg, err := run(ctx)
		if err != nil {
			logger.Error("discord command failed", zap.Any("name", name), zap.Error(err))
			msg = name + " failed, nothing changed:\n" + err.Error()
		}
		d.editOriginal(ctx, &in, msg)
	}()
	writeJSON(w, map[string]int{"type": responseDeferredChannelMessage})
}

// Wait returns when the commands started so far have finished.
func (d *Discord) Wait() { d.wg.Wait() }

// commandName is the command's name, followed by " " and the subcommand's for a subcommand.
func (in *interaction) commandName() string {
	if len(in.Data.Options) == 1 && in.Data.Options[0].Type == optionSubCommand {
		return in.Data.Name + " " + in.Data.Options[0].Name
	}
	return in.Data.Name
}

func (in *interaction) userID() string {
	if in.Member != nil {
		return in.Member.User.ID
	}
	if in.User != nil {
		return in.User.ID
	}
	return ""
}

func (d *Discord) allowed(in *interaction) bool {
	if id := in.userID(); id != "" && d.Allow[id] {
		return true
	}
	if in.Member != nil {
		for _, r := range in.Member.Roles {
			if d.Allow[r] {
				return true
			}
		}
	}
	return false
}

func (d *Discord) editOriginal(ctx context.Context, in *interaction, msg string) {
	if r := []rune(msg); len(r) > discordMessageMax {
		msg = string(r[:discordMessageMax-1]) + "…"
	}
	body, _ := json.Marshal(map[string]string{"content": msg})
	api := d.APIURL
	if api == "" {
		api = defaultDiscordAPIURL
	}
	u := api + "/webhooks/" + url.PathEscape(in.ApplicationID) + "/" + url.PathEscape(in.Token) + "/messages/@original"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		logger.Error("discord edit message failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		logger.Error("discord edit message failed", zap.Error(err))
		return
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		logger.Error("discord edit message failed", zap.String("status", res.Status))
	}
}

func reply(w http.ResponseWriter, msg string) {
	writeJSON(w, map[string]interface{}{
		"type": responseChannelMessage,
		"data": map[string]interface{}{"content": msg, "flags": messageFlagEphemeral},
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
