package db

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

var DefaultDB DB

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

func genLoginKey() string {
	return randomString(10, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
}

func genUserID() string {
	return randomString(6, "ABCDEFGHIJKLMNOPQRSTUVWXYZ23456789")
}

func genSessionID() string {
	return randomString(8, "123456789")
}

func GenBattleCode() string {
	return fmt.Sprintf("%013d", time.Now().UnixNano()/1000000)
}

type Account struct {
	LoginKey           string    `db:"login_key" json:"login_key,omitempty"`
	SessionID          string    `db:"session_id" json:"session_id,omitempty"`
	LastUserID         string    `db:"last_user_id" json:"last_user_id,omitempty"`
	Created            time.Time `db:"created" json:"created,omitempty"`
	CreatedIP          string    `db:"created_ip" json:"created_ip,omitempty"`
	LastLogin          time.Time `db:"last_login" json:"last_login,omitempty"`
	LastLoginIP        string    `db:"last_login_ip" json:"last_login_ip,omitempty"`
	LastLoginMachineID string    `db:"last_login_machine_id" json:"last_login_machine_id,omitempty"`
	System             byte      `db:"system" json:"system,omitempty"`
}

// Ban refuses a login whose user (any user of the account), machine id or ip is Value.
type Ban struct {
	Kind    string    `db:"kind" json:"kind"`
	Value   string    `db:"value" json:"value"`
	Reason  string    `db:"reason" json:"reason,omitempty"`
	Created time.Time `db:"created" json:"created"`
}

const (
	BanUser    = "user"
	BanMachine = "machine"
	BanIP      = "ip"
)

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
	Reverse    bool     `json:"reverse"` // oldest first
	Page       int      `json:"page"`
}

func NewFindReplayQuery() *FindReplayQuery {
	return &FindReplayQuery{Players: -1, Aggregate: -1}
}

type ReplayUser struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	PilotName string `json:"pilot_name"`
	Team      int    `json:"team"`
	Pos       int    `json:"pos"`
}

type FoundReplay struct {
	BattleCode string        `json:"battle_code,omitempty"`
	Users      []*ReplayUser `json:"users,omitempty"`
	Round      int           `json:"round,omitempty"`
	StartUnix  int64         `json:"start_unix,omitempty"`
	StartDate  time.Time     `json:"start_date,omitempty"`
	ReplayURL  string        `json:"replay_url,omitempty"`
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

	// RegisterAccountWithLoginKey lets users share a login key among servers.
	RegisterAccountWithLoginKey(ip string, loginKey string) (*Account, error)
	GetAccountByLoginKey(key string) (*Account, error)
	LoginAccount(*Account) error

	// SetLoginEndpoint records where the account last logged in to the lobby from.
	SetLoginEndpoint(loginKey, ip, machineID string) error
	GetAccountsByMachineID(machineID string) ([]*Account, error)

	AddBan(ban *Ban) error
	// DeleteBan returns false when there was no such ban.
	DeleteBan(kind, value string) (bool, error)
	GetBanList() ([]*Ban, error)
	// FindBan returns the ban matching the login, nil if none.
	FindBan(loginKey, ip, machineID string) (*Ban, error)
	// GetServerSetting returns the m_server_setting value of key, "" if none.
	GetServerSetting(key string) (string, error)

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

	// FindReplay returns the battles with an uploaded replay that match q, 100 per page.
	FindReplay(q *FindReplayQuery) ([]*FoundReplay, error)

	// CalculateUserTotalBattleCount counts one army's battles with side, else all.
	CalculateUserTotalBattleCount(userID string, side byte) (ret BattleCountResult, err error)
	CalculateUserDailyBattleCount(userID string) (ret BattleCountResult, err error)
	GetWinCountRanking(side byte) (ret []*RankingRecord, err error)
	GetKillCountRanking(side byte) (ret []*RankingRecord, err error)
}
