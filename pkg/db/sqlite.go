package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
)

type SQLiteDB struct {
	*sqlx.DB
	*SQLiteCache
}

type SQLiteCache struct {
	mtx          sync.Mutex
	rankingCache map[string][]*RankingRecord
}

func NewSQLiteCache() *SQLiteCache {
	return &SQLiteCache{
		rankingCache: map[string][]*RankingRecord{},
	}
}

func (c *SQLiteCache) deleteRankingCache() {
	c.mtx.Lock()
	c.rankingCache = map[string][]*RankingRecord{}
	c.mtx.Unlock()
}

const schema = `
CREATE TABLE IF NOT EXISTS account (
	login_key text,
	last_user_id text default '',
	created_ip text default '',
	created timestamp,
	last_login timestamp,
	last_login_ip text default '',
	last_login_machine_id text default '',
	system integer default 0,
	PRIMARY KEY (login_key)
);
CREATE TABLE IF NOT EXISTS auth_identity (
	provider text,
	provider_user_id text,
	login_key text,
	carried integer default 0,
	created timestamp,
	PRIMARY KEY (login_key)
);
CREATE TABLE IF NOT EXISTS auth_token (
	token_hash text,
	login_key text default '',
	provider text default '',
	provider_user_id text default '',
	created timestamp,
	PRIMARY KEY (token_hash)
);
CREATE TABLE IF NOT EXISTS user (
	user_id text,
	login_key text,
	session_id text default '',
	name text default 'default',
	team text default '',
	battle_count integer default 0,
	win_count integer default 0,
	lose_count integer default 0,
	kill_count integer default 0,
	death_count integer default 0,
	aeug_battle_count integer default 0,
	aeug_win_count integer default 0,
	aeug_lose_count integer default 0,
	aeug_kill_count integer default 0,
	aeug_death_count integer default 0,
	titans_battle_count integer default 0,
	titans_win_count integer default 0,
	titans_lose_count integer default 0,
	titans_kill_count integer default 0,
	titans_death_count integer default 0,
	daily_battle_count integer default 0,
	daily_win_count integer default 0,
	daily_lose_count integer default 0,
	created timestamp,
	system integer default 0,
	PRIMARY KEY (user_id, login_key)
);
CREATE TABLE IF NOT EXISTS battle_record (
	battle_code text,
	user_id     text,
	user_name 	text,
	pilot_name 	text,
	players     integer default 0,
	aggregate   integer default 0,
	pos         integer default 0,
	side        integer default 0,
	round       integer default 0,
	win         integer default 0,
	lose        integer default 0,
	kill        integer default 0,
	death       integer default 0,
	frame       integer default 0,
	result      text default '',
	replay_url  text default '',
	used_ms_mask integer default 0,
	used_ms_list text default '',
	play_count  integer default 0,
	created     timestamp,
	updated     timestamp,
	system      integer default 0,
	PRIMARY KEY (battle_code, user_id)
);
CREATE TABLE IF NOT EXISTS m_server_setting (
	key text PRIMARY KEY,
	value text default ''
);
CREATE TABLE IF NOT EXISTS m_lobby_setting (
	platform text,
	no integer,
	start text default '',
	explain text default '',
	rule_id text default '',
	hack text default '',
	PRIMARY KEY (platform, no)
);
CREATE TABLE IF NOT EXISTS m_rule (
	id text PRIMARY KEY,
	battle_count integer not null,
	visible_team integer not null,
	battle_time integer not null,
	battle_gauge integer not null,
	reload_type integer not null,
	damage_level integer not null,
	awake_type integer not null,
	awake_inc_ratio integer not null,
	awake_dec_ratio integer not null
);
CREATE TABLE IF NOT EXISTS m_ban (
	key text,
	until timestamp,
	reason text default '',
	created timestamp,
	PRIMARY KEY (key)
);
`

const indexes = `
CREATE INDEX IF NOT EXISTS BATTLE_RECORD_USER_ID ON battle_record(user_id);
CREATE INDEX IF NOT EXISTS BATTLE_RECORD_PLAYERS ON battle_record(players);
CREATE INDEX IF NOT EXISTS BATTLE_RECORD_CREATED ON battle_record(created);
CREATE INDEX IF NOT EXISTS BATTLE_RECORD_AGGRIGATE ON battle_record(aggregate);
CREATE INDEX IF NOT EXISTS ACCOUNT_LAST_LOGIN_MACHINE_ID ON account(last_login_machine_id);
CREATE INDEX IF NOT EXISTS AUTH_IDENTITY_PROVIDER ON auth_identity(provider, provider_user_id);
CREATE INDEX IF NOT EXISTS AUTH_TOKEN_LOGIN_KEY ON auth_token(login_key);
`

func (db SQLiteDB) Init() error {
	_, err := db.Exec(schema + indexes)
	return err
}

func (db SQLiteDB) Migrate() error {
	ctx := context.Background()
	tables := []string{"account", "user", "battle_record", "m_server_setting", "m_lobby_setting", "m_rule", "m_ban", "auth_identity", "auth_token"}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault})
	if err != nil {
		return errors.Wrap(err, "Begin failed")
	}

	_, err = tx.Exec(`DROP TABLE IF EXISTS ban`)
	if err != nil {
		tx.Rollback()
		return errors.Wrap(err, "DROP TABLE failed")
	}

	// create the tables that do not exist yet, so every one can be renamed
	_, err = tx.Exec(schema)
	if err != nil {
		tx.Rollback()
		return errors.Wrap(err, "Begin failed")
	}

	for _, table := range tables {
		tmp := table + "_tmp"
		_, err = tx.Exec(`ALTER TABLE ` + table + ` RENAME TO ` + tmp)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "ALTER TABLE failed")
		}
	}

	_, err = tx.Exec(schema)
	if err != nil {
		tx.Rollback()
		return errors.Wrap(err, "failed to create new tables")
	}

	// copy old table into new table and drop old table
	// it works unless key name is changed; columns no longer in the schema are dropped
	for _, table := range tables {
		tmp := table + "_tmp"
		oldColumns, err := tableColumns(tx, tmp)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "SELECT failed")
		}
		newColumns, err := tableColumns(tx, table)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "SELECT failed")
		}
		kept := map[string]bool{}
		for _, c := range newColumns {
			kept[c] = true
		}
		var columns []string
		for _, c := range oldColumns {
			if kept[c] {
				columns = append(columns, c)
			}
		}

		cols := strings.Join(columns, ",")
		_, err = tx.Exec(`INSERT INTO ` + table + `(` + cols + `) SELECT ` + cols + ` FROM ` + tmp)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "INSERT failed")
		}

		_, err = tx.Exec(`DROP TABLE ` + tmp)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "DROP TABLE failed")
		}
	}

	// the renamed tables kept the index names until they were dropped
	_, err = tx.Exec(indexes)
	if err != nil {
		tx.Rollback()
		return errors.Wrap(err, "failed to create indexes")
	}

	return tx.Commit()
}

func tableColumns(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}

func (db SQLiteDB) RegisterAccount(ip string) (*Account, error) {
	key := GenLoginKey()
	now := time.Now()
	_, err := db.Exec(`
INSERT INTO account
	(login_key, created_ip, created, last_login, system)
VALUES
	(?, ?, ?, ?, ?)`, key, ip, now, now, 0)
	if err != nil {
		return nil, err
	}
	a := &Account{
		LoginKey:  key,
		CreatedIP: ip,
	}
	return a, nil
}

func (db SQLiteDB) GetAccountByLoginKey(key string) (*Account, error) {
	a := &Account{}
	err := db.QueryRowx("SELECT * FROM account WHERE login_key = ?", key).StructScan(a)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (db SQLiteDB) LoginAccount(a *Account) error {
	a.LastLogin = time.Now()
	_, err := db.Exec(`UPDATE account SET last_login = ? WHERE login_key = ?`, a.LastLogin, a.LoginKey)
	return err
}

func (db SQLiteDB) SetLoginEndpoint(loginKey, ip, machineID string) error {
	_, err := db.Exec(`UPDATE account SET last_login_ip = ?, last_login_machine_id = ? WHERE login_key = ?`,
		ip, machineID, loginKey)
	return err
}

func (db SQLiteDB) FindBan(loginKey string) (*MasterBan, error) {
	bans := []*MasterBan{}
	err := db.Select(&bans, `SELECT * FROM m_ban WHERE key IN (SELECT user_id FROM user WHERE login_key = ?)`, loginKey)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, b := range bans {
		if now.Before(b.Until) {
			return b, nil
		}
	}
	return nil, nil
}

func (db SQLiteDB) ReplaceMaster(m *Master) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM m_ban`); err != nil {
		return err
	}
	for _, b := range m.Bans {
		if b.Created.IsZero() {
			b.Created = time.Now()
		}
		_, err := tx.NamedExec(`INSERT INTO m_ban (key, until, reason, created) VALUES (:key, :until, :reason, :created)`, b)
		if err != nil {
			return err
		}
	}
	if m.ServerSettings != nil {
		if _, err := tx.Exec(`DELETE FROM m_server_setting`); err != nil {
			return err
		}
		for k, v := range m.ServerSettings {
			if _, err := tx.Exec(`INSERT INTO m_server_setting (key, value) VALUES (?, ?)`, k, v); err != nil {
				return err
			}
		}
	}
	if m.LobbySettings != nil {
		if _, err := tx.Exec(`DELETE FROM m_lobby_setting`); err != nil {
			return err
		}
		for _, s := range m.LobbySettings {
			_, err := tx.NamedExec(`INSERT INTO m_lobby_setting (platform, no, start, explain, rule_id, hack) VALUES (:platform, :no, :start, :explain, :rule_id, :hack)`, s)
			if err != nil {
				return err
			}
		}
	}
	if m.Rules != nil {
		if _, err := tx.Exec(`DELETE FROM m_rule`); err != nil {
			return err
		}
		for _, r := range m.Rules {
			_, err := tx.NamedExec(`INSERT INTO m_rule (id, battle_count, visible_team, battle_time, battle_gauge, reload_type, damage_level, awake_type, awake_inc_ratio, awake_dec_ratio)
				VALUES (:id, :battle_count, :visible_team, :battle_time, :battle_gauge, :reload_type, :damage_level, :awake_type, :awake_inc_ratio, :awake_dec_ratio)`, r)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (db SQLiteDB) GetRule(id string) (*Rule, error) {
	r := &Rule{}
	err := db.QueryRowx(`SELECT * FROM m_rule WHERE id = ?`, id).StructScan(r)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (db SQLiteDB) GetMasterBans() ([]*MasterBan, error) {
	bans := []*MasterBan{}
	err := db.Select(&bans, `SELECT * FROM m_ban ORDER BY key`)
	return bans, err
}

func (db SQLiteDB) GetLobbySetting(platform string, no int) (*LobbySetting, error) {
	s := &LobbySetting{}
	err := db.QueryRowx(`SELECT * FROM m_lobby_setting WHERE platform = ? AND no = ?`, platform, no).StructScan(s)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (db SQLiteDB) GetServerSetting(key string) (string, error) {
	value := ""
	err := db.QueryRowx(`SELECT value FROM m_server_setting WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (db SQLiteDB) RegisterUser(loginKey string) (*User, error) {
	userID := genUserID()
	now := time.Now()
	res, err := db.Exec(`INSERT INTO user (user_id, login_key, created)
		SELECT ?, ?, ? WHERE (SELECT COUNT(*) FROM user WHERE login_key = ?) < ?`,
		userID, loginKey, now, loginKey, MaxUsersPerAccount)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, ErrUserLimit
	}
	u := &User{
		LoginKey: loginKey,
		UserID:   userID,
		Created:  now,
	}
	return u, nil
}

func (db SQLiteDB) GetUserList(loginKey string) ([]*User, error) {
	rows, err := db.Queryx(`SELECT * FROM user WHERE login_key = ?`, loginKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []*User{}
	for rows.Next() {
		u := new(User)
		err = rows.StructScan(u)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (db SQLiteDB) GetUser(userID string) (*User, error) {
	u := &User{}
	err := db.Get(u, `SELECT * FROM user WHERE user_id = ?`, userID)
	return u, err
}

func (db SQLiteDB) LoginUser(user *User) error {
	a, err := db.GetAccountByLoginKey(user.LoginKey)
	if err != nil {
		return err
	}
	a.LastUserID = user.UserID

	_, err = db.Exec(`UPDATE account SET last_user_id = ? WHERE login_key = ?`, a.LastUserID, a.LoginKey)
	if err != nil {
		return err
	}

	_, err = db.Exec(`UPDATE user SET session_id = ? WHERE user_id = ?`, user.SessionID, user.UserID)
	return err
}

func (db SQLiteDB) UpdateUser(user *User) error {
	_, err := db.NamedExec(`
UPDATE user
SET
	name = :name,
	team = :team,
	battle_count = :battle_count,
	win_count = :win_count,
	lose_count = :lose_count,
	kill_count = :kill_count,
	death_count = :death_count,
	aeug_battle_count = :aeug_battle_count,
	aeug_win_count = :aeug_win_count,
	aeug_lose_count = :aeug_lose_count,
	aeug_kill_count = :aeug_kill_count,
	aeug_death_count = :aeug_death_count,
	titans_battle_count = :titans_battle_count,
	titans_win_count = :titans_win_count,
	titans_lose_count = :titans_lose_count,
	titans_kill_count = :titans_kill_count,
	titans_death_count = :titans_death_count,
	daily_battle_count = :daily_battle_count,
	daily_win_count = :daily_win_count,
	daily_lose_count = :daily_lose_count,
	system = :system
WHERE
	user_id = :user_id`, user)
	return err
}

func (db SQLiteDB) AddBattleRecord(battleRecord *BattleRecord) error {
	now := time.Now()
	battleRecord.Updated = now
	battleRecord.Created = now
	_, err := db.NamedExec(`
INSERT INTO battle_record
	(battle_code, user_id, user_name, pilot_name, players, aggregate, pos, side, created, updated, system)
VALUES
	(:battle_code, :user_id, :user_name, :pilot_name, :players, :aggregate, :pos, :side, :created, :updated, :system)`,
		battleRecord)
	return err
}

func (db SQLiteDB) UpdateBattleRecord(battle *BattleRecord) error {
	battle.Updated = time.Now()
	_, err := db.NamedExec(`
UPDATE battle_record
SET
	round = :round,
	win = :win,
	lose = :lose,
	kill = :kill,
	death = :death,
	frame = :frame,
	result = :result,
	updated = :updated,
	system = :system
WHERE
	battle_code = :battle_code AND user_id = :user_id`, battle)

	if err == nil && battle.Aggregate != 0 {
		// refresh rakning page
		db.deleteRankingCache()
	}
	return err
}

func (db SQLiteDB) SetReplayURL(battleCode string, url string) error {
	_, err := db.Exec(`UPDATE battle_record SET replay_url = ? WHERE battle_code = ?`, url, battleCode)
	return err
}

func (db SQLiteDB) IncrementReplayPlayCount(battleCode string) error {
	_, err := db.Exec(`UPDATE battle_record SET play_count = play_count + 1 WHERE battle_code = ?`, battleCode)
	return err
}

func (db SQLiteDB) FindReplay(q *FindReplayQuery) ([]*FoundReplay, error) {
	where := []string{"replay_url <> ''"}
	var args []interface{}
	if q.BattleCode != "" {
		where = append(where, "battle_code = ?")
		args = append(args, q.BattleCode)
	}
	if q.Players >= 0 {
		where = append(where, "players = ?")
		args = append(args, q.Players)
	}
	if q.Aggregate >= 0 {
		where = append(where, "aggregate = ?")
		args = append(args, q.Aggregate)
	}
	var players []string
	var playerArgs []interface{}
	for _, f := range []struct {
		column, op string
		values     []string
	}{
		{"user_id", "=", q.UserIDs},
		{"user_name", "LIKE", q.UserNames},
		{"pilot_name", "LIKE", q.PilotNames},
	} {
		if len(f.values) == 0 {
			continue
		}
		var or []string
		for _, v := range f.values {
			or = append(or, f.column+" "+f.op+" ?")
			args = append(args, v)
			playerArgs = append(playerArgs, v)
		}
		players = append(players, or...)
		where = append(where, "battle_code IN (SELECT battle_code FROM battle_record WHERE "+strings.Join(or, " OR ")+")")
	}
	if q.UsedMs > 0 {
		ms := "battle_code IN (SELECT battle_code FROM battle_record WHERE (used_ms_mask & (1 << ?)) != 0"
		args = append(args, q.UsedMs)
		if len(players) > 0 {
			ms += " AND (" + strings.Join(players, " OR ") + ")"
			args = append(args, playerArgs...)
		}
		where = append(where, ms+")")
	}
	order := "DESC"
	if q.Reverse {
		order = "ASC"
	}
	page := q.Page
	if page < 0 {
		page = 0
	}
	args = append(args, page*100)

	var rs []*BattleRecord
	err := db.Select(&rs, `SELECT * FROM battle_record WHERE battle_code IN (
	SELECT battle_code FROM battle_record WHERE `+strings.Join(where, " AND ")+`
	GROUP BY battle_code ORDER BY MIN(created) `+order+` LIMIT 100 OFFSET ?)
ORDER BY created `+order, args...)
	if err != nil {
		return nil, err
	}

	var found []*FoundReplay
	byCode := map[string]*FoundReplay{}
	for _, r := range rs {
		f := byCode[r.BattleCode]
		if f == nil {
			f = &FoundReplay{BattleCode: r.BattleCode, StartUnix: r.Created.Unix(), StartDate: r.Created, ReplayURL: r.ReplayURL}
			byCode[r.BattleCode] = f
			found = append(found, f)
		}
		f.Users = append(f.Users, &ReplayUser{UserID: r.UserID, UserName: r.UserName, PilotName: r.PilotName, Team: r.Side, Pos: r.Pos, UsedMsList: r.UsedMsList})
		if f.Round < r.Round {
			f.Round = r.Round
		}
		if f.PlayCount < r.PlayCount {
			f.PlayCount = r.PlayCount
		}
	}
	for _, f := range found {
		sort.Slice(f.Users, func(i, j int) bool { return f.Users[i].Pos < f.Users[j].Pos })
	}
	return found, nil
}

func (db SQLiteDB) GetBattleRecordUser(battleCode string, userID string) (*BattleRecord, error) {
	b := new(BattleRecord)
	err := db.Get(b, `SELECT * FROM battle_record WHERE battle_code = ? AND user_id = ?`, battleCode, userID)
	return b, err
}

func (db SQLiteDB) GetBattleRecords(battleCode string) ([]*BattleRecord, error) {
	var rs []*BattleRecord
	err := db.Select(&rs, `SELECT * FROM battle_record WHERE battle_code = ? ORDER BY pos`, battleCode)
	return rs, err
}

func (db SQLiteDB) SetUsedMs(battleCode string, userID string, mask uint64, list string) error {
	_, err := db.Exec(`UPDATE battle_record SET used_ms_mask = ?, used_ms_list = ? WHERE battle_code = ? AND user_id = ?`,
		int64(mask), list, battleCode, userID)
	return err
}

func (db SQLiteDB) CalculateUserTotalBattleCount(userID string, side byte) (ret BattleCountResult, err error) {
	if side == 0 {
		r := db.QueryRow(`
			SELECT TOTAL(round), TOTAL(win), TOTAL(lose), TOTAL(kill), TOTAL(death) FROM battle_record
			WHERE user_id = ? AND aggregate <> 0 AND players = 4`, userID)
		err = r.Scan(&ret.Battle, &ret.Win, &ret.Lose, &ret.Kill, &ret.Death)
		return
	}
	r := db.QueryRow(`
		SELECT TOTAL(round), TOTAL(win), TOTAL(lose), TOTAL(kill), TOTAL(death) FROM battle_record
		WHERE user_id = ? AND aggregate <> 0 AND players = 4 AND side = ?`, userID, side)
	err = r.Scan(&ret.Battle, &ret.Win, &ret.Lose, &ret.Kill, &ret.Death)
	return
}

func (db SQLiteDB) CalculateUserDailyBattleCount(userID string) (ret BattleCountResult, err error) {
	r := db.QueryRow(`
		SELECT TOTAL(round), TOTAL(win), TOTAL(lose), TOTAL(kill), TOTAL(death) FROM battle_record
		WHERE user_id = ? AND aggregate <> 0 AND players = 4 AND created > ?`,
		userID, time.Now().AddDate(0, 0, -1))
	err = r.Scan(&ret.Battle, &ret.Win, &ret.Lose, &ret.Kill, &ret.Death)
	return
}

func (db SQLiteDB) GetWinCountRanking(side byte) ([]*RankingRecord, error) {
	cacheKey := fmt.Sprint("win", side)
	db.mtx.Lock()
	ranking, ok := db.rankingCache[cacheKey]
	db.mtx.Unlock()
	if ok {
		return ranking, nil
	}

	var rows *sqlx.Rows
	var err error

	target := "win_count"
	if side == 1 {
		target = "aeug_win_count"
	} else if side == 2 {
		target = "titans_win_count"
	}

	rows, err = db.Queryx(`
		SELECT RANK() OVER(ORDER BY `+target+` DESC) as rank,
		user_id, name, team,
		battle_count, win_count, lose_count, kill_count, death_count,
		aeug_battle_count, aeug_win_count, aeug_lose_count, aeug_kill_count, aeug_death_count,
		titans_battle_count, titans_win_count, titans_lose_count, titans_kill_count, titans_death_count
		FROM user ORDER BY rank LIMIT ?`, 100)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ranking = []*RankingRecord{}
	for rows.Next() {
		u := new(RankingRecord)
		err = rows.StructScan(u)
		if err != nil {
			return nil, err
		}
		if !utf8.ValidString(u.Name) {
			u.Name = "？"
		}
		if !utf8.ValidString(u.Team) {
			u.Team = "？"
		}
		ranking = append(ranking, u)
	}

	db.mtx.Lock()
	db.rankingCache[cacheKey] = ranking
	db.mtx.Unlock()

	return ranking, nil
}

func (db SQLiteDB) GetKillCountRanking(side byte) ([]*RankingRecord, error) {
	cacheKey := fmt.Sprint("kill", side)
	db.mtx.Lock()
	ranking, ok := db.rankingCache[cacheKey]
	db.mtx.Unlock()
	if ok {
		return ranking, nil
	}

	var rows *sqlx.Rows
	var err error

	target := "kill_count"
	if side == 1 {
		target = "aeug_kill_count"
	} else if side == 2 {
		target = "titans_kill_count"
	}

	rows, err = db.Queryx(`
		SELECT RANK() OVER(ORDER BY `+target+` DESC) as rank,
		user_id, name, team,
		battle_count, win_count, lose_count, kill_count, death_count,
		aeug_battle_count, aeug_win_count, aeug_lose_count, aeug_kill_count, aeug_death_count,
		titans_battle_count, titans_win_count, titans_lose_count, titans_kill_count, titans_death_count
		FROM user ORDER BY rank LIMIT ?`, 100)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ranking = []*RankingRecord{}
	for rows.Next() {
		u := new(RankingRecord)
		err = rows.StructScan(u)
		if err != nil {
			return nil, err
		}
		if !utf8.ValidString(u.Name) {
			u.Name = "？"
		}
		if !utf8.ValidString(u.Team) {
			u.Team = "？"
		}
		ranking = append(ranking, u)
	}

	db.mtx.Lock()
	db.rankingCache[cacheKey] = ranking
	db.mtx.Unlock()

	return ranking, nil
}

func (db SQLiteDB) GetLobbySettings() ([]*LobbySetting, error) {
	settings := []*LobbySetting{}
	err := db.Select(&settings, `SELECT * FROM m_lobby_setting ORDER BY platform, no`)
	return settings, err
}
