package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/pkg/errors"
)

var (
	ErrIdentityFull  = errors.New("the identity has made the most accounts")
	ErrCarryFull     = errors.New("the identity has carried over the most accounts")
	ErrAccountLinked = errors.New("the account is already linked")
	ErrUnknownToken  = errors.New("unknown or revoked token")
	ErrNoIdentity    = errors.New("the token has no provider identity")
)

// Per provider identity: accounts made for it in the emulator, and accounts carried over from a PS2.
const (
	MaxIdentityNewAccounts     = 2
	MaxIdentityCarriedAccounts = 2
)

// CarryMinBattles is the battle count (over the account's users) a carry-over code needs.
const CarryMinBattles = 100

func hashAuthToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func genAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (db SQLiteDB) IdentityLoginKey(provider, providerUserID string) (string, error) {
	keys, err := identityKeys(db.DB, provider, providerUserID)
	if err != nil || len(keys) == 0 {
		return "", err
	}
	return keys[0], nil
}

type querier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

func identityKeys(q querier, provider, providerUserID string) ([]string, error) {
	rows, err := q.Query("SELECT login_key FROM auth_identity WHERE provider = ? AND provider_user_id = ? ORDER BY created, rowid",
		provider, providerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// tokenIdentity returns the token's identity and the account it was issued for ("" for an identity
// token). A token issued for an account holds no identity: it is the account's link, if any.
func (db SQLiteDB) tokenIdentity(token string) (provider, uid, key string, err error) {
	err = db.QueryRow("SELECT provider, provider_user_id, login_key FROM auth_token WHERE token_hash = ?",
		hashAuthToken(token)).Scan(&provider, &uid, &key)
	if err == sql.ErrNoRows {
		return "", "", "", ErrUnknownToken
	}
	if err != nil || provider != "" || key == "" {
		return provider, uid, key, err
	}
	err = db.QueryRow("SELECT provider, provider_user_id FROM auth_identity WHERE login_key = ?", key).Scan(&provider, &uid)
	if err == sql.ErrNoRows {
		err = nil
	}
	return provider, uid, key, err
}

func (db SQLiteDB) TokenAccounts(token string) (string, []string, error) {
	provider, uid, key, err := db.tokenIdentity(token)
	if err != nil {
		return "", nil, err
	}
	if provider == "" {
		return key, []string{key}, nil
	}
	keys, err := identityKeys(db.DB, provider, uid)
	if err != nil {
		return "", nil, err
	}
	for _, k := range keys {
		if k == key {
			return key, keys, nil
		}
	}
	if len(keys) == 0 {
		return "", keys, nil
	}
	return keys[0], keys, nil
}

func (db SQLiteDB) AddTokenAccount(token, ip string) (*DBAccount, error) {
	provider, uid, _, err := db.tokenIdentity(token)
	if err != nil {
		return nil, err
	}
	if provider == "" {
		return nil, ErrNoIdentity
	}
	return db.RegisterIdentityAccount(provider, uid, ip)
}

func (db SQLiteDB) CarryTokenAccount(token, loginKey string) error {
	provider, uid, _, err := db.tokenIdentity(token)
	if err != nil {
		return err
	}
	if provider == "" {
		return ErrNoIdentity
	}
	return db.LinkIdentity(provider, uid, loginKey)
}

func (db SQLiteDB) RegisterIdentityAccount(provider, providerUserID, ip string) (*DBAccount, error) {
	tx, err := db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM auth_identity WHERE provider = ? AND provider_user_id = ? AND carried = 0",
		provider, providerUserID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxIdentityNewAccounts {
		return nil, ErrIdentityFull
	}
	key := GenLoginKey()
	now := time.Now()
	if _, err := tx.Exec(`INSERT INTO account (login_key, created_ip, created, last_login, system) VALUES (?, ?, ?, ?, 0)`,
		key, ip, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO auth_identity (provider, provider_user_id, login_key, created) VALUES (?, ?, ?, ?)`,
		provider, providerUserID, key, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &DBAccount{LoginKey: key, CreatedIP: ip, Created: now, LastLogin: now}, nil
}

func (db SQLiteDB) LinkIdentity(provider, providerUserID, loginKey string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM auth_identity WHERE provider = ? AND provider_user_id = ? AND carried = 1",
		provider, providerUserID).Scan(&n); err != nil {
		return err
	}
	if n >= MaxIdentityCarriedAccounts {
		return ErrCarryFull
	}
	if err := tx.QueryRow("SELECT COUNT(*) FROM auth_identity WHERE login_key = ?", loginKey).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrAccountLinked
	}
	if err := tx.QueryRow("SELECT COUNT(*) FROM account WHERE login_key = ?", loginKey).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`INSERT INTO auth_identity (provider, provider_user_id, login_key, carried, created) VALUES (?, ?, ?, 1, ?)`,
		provider, providerUserID, loginKey, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (db SQLiteDB) IssueAuthToken(provider, providerUserID string) (string, error) {
	token, err := genAuthToken()
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`INSERT INTO auth_token (token_hash, provider, provider_user_id, created) VALUES (?, ?, ?, ?)`,
		hashAuthToken(token), provider, providerUserID, time.Now())
	if err != nil {
		return "", err
	}
	return token, nil
}

func (db SQLiteDB) RevokeAuthToken(token string) error {
	_, err := db.Exec("DELETE FROM auth_token WHERE token_hash = ?", hashAuthToken(token))
	return err
}

func (db SQLiteDB) AccountCarried(loginKey string) (bool, error) {
	var carried bool
	err := db.QueryRow("SELECT carried FROM auth_identity WHERE login_key = ?", loginKey).Scan(&carried)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return carried, err
}

func (db SQLiteDB) AccountBattleCount(loginKey string) (int, error) {
	var n int
	err := db.QueryRow("SELECT COALESCE(SUM(battle_count), 0) FROM user WHERE login_key = ?", loginKey).Scan(&n)
	return n, err
}
