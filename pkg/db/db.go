package db

import (
	"bytes"
	"fmt"
	"math/rand"
	"time"
)

var DefaultDB DB

func randomString(length int, source string) string {
	var result bytes.Buffer
	for i := 0; i < length; i++ {
		index := rand.Intn(len(source))
		result.WriteByte(source[index])
	}
	return result.String()
}

func genLoginKey() string {
	return randomString(10, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
}

func genUserId() string {
	return randomString(6, "ABCDEFGHIJKLMNOPQRSTUVWXYZ23456789")
}

func genSessionId() string {
	return randomString(8, "123456789")
}

func GenBattleCode() string {
	return fmt.Sprintf("%013d", time.Now().UnixNano()/1000000)
}

func randInt(min int, max int) int {
	rand.Seed(time.Now().UTC().UnixNano())
	return min + rand.Intn(max-min)
}

type Account struct {
	LoginKey   string    `db:"login_key" json:"login_key,omitempty"`
	SessionId  string    `db:"session_id" json:"session_id,omitempty"`
	LastUserId string    `db:"last_user_id" json:"last_user_id,omitempty"`
	Created    time.Time `db:"created" json:"created,omitempty"`
	CreatedIP  string    `db:"created_ip" json:"created_ip,omitempty"`
	LastLogin  time.Time `db:"last_login" json:"last_login,omitempty"`
	System     byte      `db:"system" json:"system,omitempty"`
}

type User struct {
	LoginKey  string `db:"login_key" json:"login_key,omitempty"`
	SessionId string `db:"session_id" json:"session_id,omitempty"`

	UserId string `db:"user_id" json:"user_id,omitempty"`
	Name   string `db:"name" json:"name,omitempty"`
	Team   string `db:"team" json:"team,omitempty"`

	BattleCount      int `db:"battle_count" json:"battle_count,omitempty"`
	WinCount         int `db:"win_count" json:"win_count,omitempty"`
	LoseCount        int `db:"lose_count" json:"lose_count,omitempty"`
	KillCount        int `db:"kill_count" json:"kill_count,omitempty"`
	DeathCount       int `db:"death_count" json:"death_count,omitempty"`
	DailyBattleCount int `db:"daily_battle_count" json:"daily_battle_count,omitempty"`
	DailyWinCount    int `db:"daily_win_count" json:"daily_win_count,omitempty"`
	DailyLoseCount   int `db:"daily_lose_count" json:"daily_lose_count,omitempty"`

	Created time.Time `db:"created" json:"created,omitempty"`
	System  uint32    `db:"system" json:"system,omitempty"`
}

type BattleRecord struct {
	BattleCode string `db:"battle_code" json:"battle_code,omitempty"`
	UserId     string `db:"user_id" json:"user_id,omitempty"`
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

	Created time.Time `db:"created" json:"created,omitempty"`
	Updated time.Time `db:"updated" json:"updated,omitempty"`
	System  uint32    `db:"system" json:"system,omitempty"`
}

type BattleCountResult struct {
	Battle int `json:"battle,omitempty"`
	Win    int `json:"win,omitempty"`
	Lose   int `json:"lose,omitempty"`
	Kill   int `json:"kill,omitempty"`
	Death  int `json:"death,omitempty"`
}

type DB interface {
	Init() error
	Migrate() error
	RegisterAccount(ip string) (*Account, error)
	RegisterAccountWithLoginKey(ip string, loginKey string) (*Account, error)
	GetAccountByLoginKey(key string) (*Account, error)
	LoginAccount(*Account) error
	RegisterUser(loginKey string) (*User, error)
	GetUserList(loginKey string) ([]*User, error)
	GetUser(userId string) (*User, error)
	LoginUser(user *User) error
	UpdateUser(user *User) error
	AddBattleRecord(battle *BattleRecord) error
	GetBattleRecordUser(battleCode string, userId string) (*BattleRecord, error)
	UpdateBattleRecord(record *BattleRecord) error
	CalculateUserTotalBattleCount(userId string, side byte) (ret BattleCountResult, err error)
	CalculateUserDailyBattleCount(userId string) (ret BattleCountResult, err error)
}
