package db

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"
)

var DefaultDB DB

const MaxUsersPerAccount = 3

var ErrUserLimit = errors.New("account already has the maximum number of users")

// randomString draws from crypto/rand: login keys and session ids are credentials.
func randomString(length int, source string) string {
	b := make([]byte, length)
	n := big.NewInt(int64(len(source)))
	for i := range b {
		index, err := rand.Int(rand.Reader, n)
		if err != nil {
			panic(err)
		}
		b[i] = source[index.Int64()]
	}
	return string(b)
}

// GenLoginKey makes a login key, or a temporary key in its place.
func GenLoginKey() string {
	return randomString(10, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
}

func genUserID() string {
	return randomString(6, "ABCDEFGHIJKLMNOPQRSTUVWXYZ23456789")
}

// GenSessionID makes a session id the game can carry: 8 digits, no 0.
func GenSessionID() string {
	return randomString(8, "123456789")
}

func GenBattleCode() string {
	return fmt.Sprintf("%013d", time.Now().UnixNano()/1000000)
}

type Account struct {
	LoginKey           string    `db:"login_key" json:"login_key,omitempty"`
	LastUserID         string    `db:"last_user_id" json:"last_user_id,omitempty"`
	Created            time.Time `db:"created" json:"created,omitempty"`
	CreatedIP          string    `db:"created_ip" json:"created_ip,omitempty"`
	LastLogin          time.Time `db:"last_login" json:"last_login,omitempty"`
	LastLoginIP        string    `db:"last_login_ip" json:"last_login_ip,omitempty"`
	LastLoginMachineID string    `db:"last_login_machine_id" json:"last_login_machine_id,omitempty"`
	System             byte      `db:"system" json:"system,omitempty"`
}

// MasterBan is a row of m_ban, the spreadsheet's bans: every user of the account of user Key
// is refused until Until.
type MasterBan struct {
	Key     string    `db:"key" json:"key"`
	Until   time.Time `db:"until" json:"until"`
	Reason  string    `db:"reason" json:"reason,omitempty"`
	Created time.Time `db:"created" json:"created"`
}

// Master is the master data loaded from the spreadsheet; ReplaceMaster replaces all of it.
type Master struct {
	Bans []*MasterBan
	// ServerSettings replaces m_server_setting; nil keeps it.
	ServerSettings map[string]string
	// LobbySettings replaces m_lobby_setting; nil keeps it.
	LobbySettings []*LobbySetting
	// Rules replaces m_rule; nil keeps it.
	Rules []*Rule
}

type LobbySetting struct {
	Platform string `db:"platform"`
	No       int    `db:"no"`
	Start    string `db:"start"`
	Explain  string `db:"explain"`
	// RuleID is the m_rule of the lobby's battles; empty: the default rule.
	RuleID string `db:"rule_id"`
	// Hack lists the lobby's special uses (model.Hack*), comma separated; empty: none.
	Hack string `db:"hack"`
}

type Rule struct {
	ID            string `db:"id"`
	BattleCount   uint16 `db:"battle_count"`
	VisibleTeam   uint16 `db:"visible_team"`
	BattleTime    uint16 `db:"battle_time"`
	BattleGauge   uint16 `db:"battle_gauge"`
	ReloadType    uint16 `db:"reload_type"`
	DamageLevel   uint16 `db:"damage_level"`
	AwakeType     uint16 `db:"awake_type"`
	AwakeIncRatio uint16 `db:"awake_inc_ratio"`
	AwakeDecRatio uint16 `db:"awake_dec_ratio"`
}

// Values maps each rule key (model.RuleKeys) to its field.
func (r *Rule) Values() map[string]*uint16 {
	return map[string]*uint16{
		"battle_count":    &r.BattleCount,
		"visible_team":    &r.VisibleTeam,
		"battle_time":     &r.BattleTime,
		"battle_gauge":    &r.BattleGauge,
		"reload_type":     &r.ReloadType,
		"damage_level":    &r.DamageLevel,
		"awake_type":      &r.AwakeType,
		"awake_inc_ratio": &r.AwakeIncRatio,
		"awake_dec_ratio": &r.AwakeDecRatio,
	}
}

type User struct {
	LoginKey  string `db:"login_key" json:"login_key,omitempty"`
	SessionID string `db:"session_id" json:"session_id,omitempty"`

	UserID string `db:"user_id" json:"user_id,omitempty"`
	Name   string `db:"name" json:"name,omitempty"`
	Team   string `db:"team" json:"team,omitempty"`

	BattleCount       int `db:"battle_count" json:"battle_count,omitempty"`
	WinCount          int `db:"win_count" json:"win_count,omitempty"`
	LoseCount         int `db:"lose_count" json:"lose_count,omitempty"`
	KillCount         int `db:"kill_count" json:"kill_count,omitempty"`
	DeathCount        int `db:"death_count" json:"death_count,omitempty"`
	AeugBattleCount   int `db:"aeug_battle_count" json:"aeug_battle_count,omitempty"`
	AeugWinCount      int `db:"aeug_win_count" json:"aeug_win_count,omitempty"`
	AeugLoseCount     int `db:"aeug_lose_count" json:"aeug_lose_count,omitempty"`
	AeugKillCount     int `db:"aeug_kill_count" json:"aeug_kill_count,omitempty"`
	AeugDeathCount    int `db:"aeug_death_count" json:"aeug_death_count,omitempty"`
	TitansBattleCount int `db:"titans_battle_count" json:"titans_battle_count,omitempty"`
	TitansWinCount    int `db:"titans_win_count" json:"titans_win_count,omitempty"`
	TitansLoseCount   int `db:"titans_lose_count" json:"titans_lose_count,omitempty"`
	TitansKillCount   int `db:"titans_kill_count" json:"titans_kill_count,omitempty"`
	TitansDeathCount  int `db:"titans_death_count" json:"titans_death_count,omitempty"`

	DailyBattleCount int `db:"daily_battle_count" json:"daily_battle_count,omitempty"`
	DailyWinCount    int `db:"daily_win_count" json:"daily_win_count,omitempty"`
	DailyLoseCount   int `db:"daily_lose_count" json:"daily_lose_count,omitempty"`

	Created time.Time `db:"created" json:"created,omitempty"`
	System  uint32    `db:"system" json:"system,omitempty"`
}

type BattleRecord struct {
	BattleCode string `db:"battle_code" json:"battle_code,omitempty"`
	UserID     string `db:"user_id" json:"user_id,omitempty"`
	UserName   string `db:"user_name" json:"user_name,omitempty"`
	PilotName  string `db:"pilot_name" json:"pilot_name,omitempty"`
	Players    int    `db:"players" json:"players,omitempty"`
	Aggregate  int    `db:"aggregate" json:"aggregate,omitempty"`

	Pos    int    `db:"pos" json:"pos,omitempty"`
	Side   int    `db:"side" json:"side,omitempty"`
	Round  int    `db:"round" json:"round,omitempty"`
	Win    int    `db:"win" json:"win,omitempty"`
	Lose   int    `db:"lose" json:"lose,omitempty"`
	Kill   int    `db:"kill" json:"kill,omitempty"`
	Death  int    `db:"death" json:"death,omitempty"`
	Frame  int    `db:"frame" json:"frame,omitempty"`
	Result string `db:"result" json:"result,omitempty"`
	// ReplayURL is where the battle's replay was uploaded (SetReplayURL), "" if none.
	ReplayURL string `db:"replay_url" json:"replay_url,omitempty"`
	// UsedMsMask has bit n set for each MS id + 1 = n the player used; UsedMsList: those per round, comma separated.
	UsedMsMask uint64 `db:"used_ms_mask" json:"used_ms_mask,omitempty"`
	UsedMsList string `db:"used_ms_list" json:"used_ms_list,omitempty"`
	// PlayCount: how often the replay was played (IncrementReplayPlayCount), the same on every player's record.
	PlayCount int `db:"play_count" json:"play_count,omitempty"`

	Created time.Time `db:"created" json:"created,omitempty"`
	Updated time.Time `db:"updated" json:"updated,omitempty"`
	System  uint32    `db:"system" json:"system,omitempty"`
}

// FindReplayQuery filters FindReplay. A battle matches each player filter when one of its
// players matches one of the values (names are LIKE patterns); -1 = any players / aggregate.
type FindReplayQuery struct {
	BattleCode string   `json:"battle_code"`
	UserIDs    []string `json:"user_id"`
	UserNames  []string `json:"user_name"`
	PilotNames []string `json:"pilot_name"`
	Players    int      `json:"players"`
	Aggregate  int      `json:"aggregate"`
	UsedMs     int      `json:"used_ms"` // an MS id + 1 a matched player (any player without player filters) used; 0 = any
	Reverse    bool     `json:"reverse"` // oldest first
	Page       int      `json:"page"`
}

func NewFindReplayQuery() *FindReplayQuery {
	return &FindReplayQuery{Players: -1, Aggregate: -1}
}

type ReplayUser struct {
	UserID     string `json:"user_id"`
	UserName   string `json:"user_name"`
	PilotName  string `json:"pilot_name"`
	Team       int    `json:"team"`
	Pos        int    `json:"pos"`
	UsedMsList string `json:"used_ms_list,omitempty"`
}

type FoundReplay struct {
	BattleCode string        `json:"battle_code,omitempty"`
	Users      []*ReplayUser `json:"users,omitempty"`
	Round      int           `json:"round,omitempty"`
	StartUnix  int64         `json:"start_unix,omitempty"`
	StartDate  time.Time     `json:"start_date,omitempty"`
	ReplayURL  string        `json:"replay_url,omitempty"`
	PlayCount  int           `json:"play_count"`
}

type BattleCountResult struct {
	Battle int `json:"battle,omitempty"`
	Win    int `json:"win,omitempty"`
	Lose   int `json:"lose,omitempty"`
	Kill   int `json:"kill,omitempty"`
	Death  int `json:"death,omitempty"`
}

type RankingRecord struct {
	Rank int `db:"rank"`
	User
}

type DB interface {
	Init() error
	Migrate() error
	RegisterAccount(ip string) (*Account, error)

	GetAccountByLoginKey(key string) (*Account, error)
	// IdentityLoginKey returns the first login key linked to a provider identity, "" when none.
	IdentityLoginKey(provider, providerUserID string) (string, error)
	// RegisterIdentityAccount creates an account linked to a provider identity;
	// ErrIdentityFull when it made MaxIdentityNewAccounts.
	RegisterIdentityAccount(provider, providerUserID, ip string) (*Account, error)
	// LinkIdentity links a provider identity to an existing account (carried over); ErrCarryFull when
	// the identity carried MaxIdentityCarriedAccounts, ErrAccountLinked when the account has a link.
	LinkIdentity(provider, providerUserID, loginKey string) error
	// AccountCarried tells whether the account was carried over to its identity.
	AccountCarried(loginKey string) (bool, error)
	// AccountBattleCount is the battle count summed over the account's users.
	AccountBattleCount(loginKey string) (int, error)
	// TokenAccounts returns the token's default login key and the keys of its identity in the
	// order they were linked. The default is the account the token was issued for, else the first;
	// "" when the identity has none. ErrUnknownToken when unknown or revoked.
	TokenAccounts(token string) (string, []string, error)
	// AddTokenAccount creates another account for the token's identity.
	AddTokenAccount(token, ip string) (*Account, error)
	// CarryTokenAccount links an existing account to the token's identity (see LinkIdentity).
	CarryTokenAccount(token, loginKey string) error
	// IssueAuthToken stores a new emulator token for a provider identity and returns it.
	IssueAuthToken(provider, providerUserID string) (string, error)
	RevokeAuthToken(token string) error
	// LoginAccount records a login of the account at the login pages.
	LoginAccount(*Account) error

	// SetLoginEndpoint records where the account last logged in to the lobby from.
	SetLoginEndpoint(loginKey, ip, machineID string) error

	// FindBan returns an unexpired m_ban of a user of the account, nil if none.
	FindBan(loginKey string) (*MasterBan, error)
	// GetServerSetting returns the m_server_setting value of key, "" if none.
	GetServerSetting(key string) (string, error)
	// GetLobbySetting returns the m_lobby_setting of a lobby, nil if none.
	GetLobbySetting(platform string, no int) (*LobbySetting, error)
	// GetLobbySettings returns all of m_lobby_setting.
	GetLobbySettings() ([]*LobbySetting, error)
	// GetRule returns the m_rule of id, nil if none.
	GetRule(id string) (*Rule, error)
	// ReplaceMaster replaces every m_ table of m in one transaction.
	ReplaceMaster(m *Master) error
	GetMasterBans() ([]*MasterBan, error)

	// An account can hold three users.
	RegisterUser(loginKey string) (*User, error)
	GetUserList(loginKey string) ([]*User, error)
	GetUser(userID string) (*User, error)
	LoginUser(user *User) error
	UpdateUser(user *User) error

	// AddBattleRecord is called when a battle starts.
	AddBattleRecord(battle *BattleRecord) error
	GetBattleRecordUser(battleCode string, userID string) (*BattleRecord, error)
	UpdateBattleRecord(record *BattleRecord) error

	// SetReplayURL sets the replay url of every player's record of the battle.
	SetReplayURL(battleCode string, url string) error
	// IncrementReplayPlayCount counts one play of the battle's replay; no error for an unknown battle.
	IncrementReplayPlayCount(battleCode string) error

	// FindReplay returns the battles with an uploaded replay that match q, 100 per page.
	FindReplay(q *FindReplayQuery) ([]*FoundReplay, error)

	// SetUsedMs sets the used_ms_mask and used_ms_list of a player's record of the battle.
	SetUsedMs(battleCode string, userID string, mask uint64, list string) error
	// GetBattleRecords returns every player's record of the battle.
	GetBattleRecords(battleCode string) ([]*BattleRecord, error)

	// CalculateUserTotalBattleCount counts one army's battles with side, else all.
	CalculateUserTotalBattleCount(userID string, side byte) (ret BattleCountResult, err error)
	CalculateUserDailyBattleCount(userID string) (ret BattleCountResult, err error)
	GetWinCountRanking(side byte) (ret []*RankingRecord, err error)
	GetKillCountRanking(side byte) (ret []*RankingRecord, err error)
}
