package db

import (
	"database/sql"
	"time"

	_ "github.com/golang/glog"
	_ "github.com/mattn/go-sqlite3"
)

type SQLiteDB struct {
	*sql.DB
}

func (db SQLiteDB) Init() error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS account (
        login_key text,
        session_id text default '',
        last_user_id text default '',
        created_ip text default '',
        created timestamp,
        last_login timestamp,
        system integer default 0,
        PRIMARY KEY (login_key)
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
        daily_battle_count integer default 0,
        daily_win_count integer default 0,
        daily_lose_count integer default 0,
        created timestamp,
        system integer default 0,
        PRIMARY KEY (user_id, login_key)
);
CREATE TABLE IF NOT EXISTS battle_user (
        user_id text,
        session_id text,
        name text default '',
        team text default ''
);
CREATE INDEX BATTLE_SESSION_ID ON battle_user(session_id);
`)
	return err
}

func (db SQLiteDB) RegisterAccount(ip string) (*Account, error) {
	key := genLoginKey()
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

func (db SQLiteDB) RegisterAccountWithLoginKey(ip string, loginKey string) (*Account, error) {
	now := time.Now()
	_, err := db.Exec(`
INSERT INTO account
	(login_key, created_ip, created, last_login, system)
VALUES
	(?, ?, ?, ?, ?)`, loginKey, ip, now, now, 0)
	if err != nil {
		return nil, err
	}
	a := &Account{
		LoginKey:  loginKey,
		CreatedIP: ip,
	}
	return a, nil
}

func (db SQLiteDB) GetAccountByLoginKey(key string) (*Account, error) {
	a := &Account{}
	r := db.QueryRow(`
SELECT
	login_key,
	session_id,
	last_user_id,
	created,
	created_ip,
	last_login,
	system
FROM
	account
WHERE
	login_key = ?`, key)
	err := r.Scan(
		&a.LoginKey,
		&a.SessionId,
		&a.LastUserId,
		&a.Created,
		&a.CreatedIP,
		&a.LastLogin,
		&a.System)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (db SQLiteDB) LoginAccount(a *Account) error {
	a.SessionId = genSessionId()
	a.LastLogin = time.Now()
	_, err := db.Exec(`
UPDATE
	account
SET
	session_id = ?,
	last_login = ?
WHERE
	login_key = ?`,
		a.SessionId,
		a.LastLogin,
		a.LoginKey)
	return err
}

func (db SQLiteDB) RegisterUser(loginKey string) (*User, error) {
	userId := genUserId()
	now := time.Now()
	_, err := db.Exec(`
INSERT INTO user
	(user_id, login_key, created) 
VALUES
	(?, ?, ?)`, userId, loginKey, now)
	if err != nil {
		return nil, err
	}
	u := &User{
		LoginKey: loginKey,
		UserId:   userId,
		Created:  now,
	}
	return u, nil
}

func (db SQLiteDB) GetUserList(loginKey string) ([]*User, error) {
	rows, err := db.Query(`
SELECT
	user_id,
	login_key,
	name,
	team,
	created,
	battle_count,
	win_count,
	lose_count,
	daily_battle_count,
	daily_win_count,
	daily_lose_count,
	system
FROM
	user
WHERE
	login_key = ?`, loginKey)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]*User, 0)
	for rows.Next() {
		u := new(User)
		err = rows.Scan(
			&u.UserId,
			&u.LoginKey,
			&u.Name,
			&u.Team,
			&u.Created,
			&u.BattleCount,
			&u.WinCount,
			&u.LoseCount,
			&u.DailyBattleCount,
			&u.DailyWinCount,
			&u.DailyLoseCount,
			&u.System)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	if err != nil {
		return nil, err
	}
	return users, nil
}

func (db SQLiteDB) GetUser(userId string) (*User, error) {
	u := &User{}
	r := db.QueryRow(`
SELECT
	user_id,
	login_key,
	name,
	team,
	created,
	battle_count,
	win_count,
	lose_count,
	daily_battle_count,
	daily_win_count,
	daily_lose_count,
	system
FROM
	user
WHERE
	user_id = ?`, userId)
	err := r.Scan(
		&u.UserId,
		&u.LoginKey,
		&u.Name,
		&u.Team,
		&u.Created,
		&u.BattleCount,
		&u.WinCount,
		&u.LoseCount,
		&u.DailyBattleCount,
		&u.DailyWinCount,
		&u.DailyLoseCount,
		&u.System)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db SQLiteDB) LoginUser(user *User) error {
	a, err := db.GetAccountByLoginKey(user.LoginKey)
	if err != nil {
		return err
	}
	a.LastUserId = user.UserId

	_, err = db.Exec(`
UPDATE
	account
SET
	last_user_id = ?
WHERE
	login_key = ?`,
		a.LastUserId,
		a.LoginKey)

	if err != nil {
		return err
	}

	_, err = db.Exec(`
UPDATE
	user
SET
	session_id = ?
WHERE
	user_id = ?`,
		user.SessionId,
		user.UserId)
	return err
}

func (db SQLiteDB) UpdateUser(user *User) error {
	_, err := db.Exec(`
UPDATE
	user
SET
	name = ?,
	team = ?,
	battle_count = ?,
	win_count = ?,
	lose_count = ?,
	daily_battle_count = ?,
	daily_win_count = ?,
	daily_lose_count = ?,
	system = ?
WHERE
	user_id = ?`,
		user.Name,
		user.Team,
		user.BattleCount,
		user.WinCount,
		user.LoseCount,
		user.DailyBattleCount,
		user.DailyWinCount,
		user.DailyLoseCount,
		user.System,
		user.UserId)
	return err
}
