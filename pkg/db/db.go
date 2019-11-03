package db

import (
	"bytes"
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

func randInt(min int, max int) int {
	rand.Seed(time.Now().UTC().UnixNano())
	return min + rand.Intn(max-min)
}

type Account struct {
	LoginKey   string
	SessionId  string
	LastUserId string
	Created    time.Time
	CreatedIP  string
	LastLogin  time.Time
	System     byte
}

type User struct {
	LoginKey  string
	SessionId string

	UserId string
	Name   string
	Team   string

	BattleCount int
	WinCount    int
	LoseCount   int

	DailyBattleCount int
	DailyWinCount    int
	DailyLoseCount   int

	Created time.Time
	System  uint32
}

type DB interface {
	Init() error
	RegisterAccount(ip string) (*Account, error)
	GetAccountByLoginKey(key string) (*Account, error)
	LoginAccount(*Account) error
	RegisterUser(loginKey string) (*User, error)
	GetUserList(loginKey string) ([]*User, error)
	GetUser(userId string) (*User, error)
	LoginUser(user *User) error
	UpdateUser(user *User) error
}
