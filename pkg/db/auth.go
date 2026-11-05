package db

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
	ErrIdentityFull  = errors.New("the identity has the most accounts")
	ErrAccountLinked = errors.New("the account is already linked")
)

// MaxIdentityAccounts is how many accounts one provider identity can hold.
const MaxIdentityAccounts = 5

const tempKeyTries = 8

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

func (db SQLiteDB) GetAccountByLoginOrTempKey(key string) (*Account, error) {
	a := &Account{}
	err := db.QueryRowx("SELECT * FROM account WHERE login_key = ? OR temp_key = ? LIMIT 1", key, key).StructScan(a)
	if err != nil {
		return nil, err
	}
	return a, nil
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

func (db SQLiteDB) TokenAccounts(token string) (string, []string, error) {
	key, err := db.AuthTokenLoginKey(token)
	if err != nil || key == "" {
		return "", nil, err
	}
	var provider, uid string
	err = db.QueryRow("SELECT provider, provider_user_id FROM auth_identity WHERE login_key = ?", key).Scan(&provider, &uid)
	if err == sql.ErrNoRows {
		return key, []string{key}, nil
	}
	if err != nil {
		return "", nil, err
	}
	keys, err := identityKeys(db.DB, provider, uid)
	if err != nil {
		return "", nil, err
	}
	return key, keys, nil
}

func (db SQLiteDB) AddTokenAccount(token, ip string) (*Account, error) {
	key, err := db.AuthTokenLoginKey(token)
	if err != nil {
		return nil, err
	}
	var provider, uid string
	if err := db.QueryRow("SELECT provider, provider_user_id FROM auth_identity WHERE login_key = ?", key).Scan(&provider, &uid); err != nil {
		return nil, err
	}
	return db.RegisterIdentityAccount(provider, uid, ip)
}

func (db SQLiteDB) RegisterIdentityAccount(provider, providerUserID, ip string) (*Account, error) {
	tx, err := db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM auth_identity WHERE provider = ? AND provider_user_id = ?",
		provider, providerUserID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxIdentityAccounts {
		return nil, ErrIdentityFull
	}
	key := genLoginKey()
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
	return &Account{LoginKey: key, CreatedIP: ip, Created: now, LastLogin: now}, nil
}

func (db SQLiteDB) LinkIdentity(provider, providerUserID, loginKey string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM auth_identity WHERE provider = ? AND provider_user_id = ?",
		provider, providerUserID).Scan(&n); err != nil {
		return err
	}
	if n >= MaxIdentityAccounts {
		return ErrIdentityFull
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
	if _, err := tx.Exec(`INSERT INTO auth_identity (provider, provider_user_id, login_key, created) VALUES (?, ?, ?, ?)`,
		provider, providerUserID, loginKey, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (db SQLiteDB) IssueAuthToken(loginKey string) (string, error) {
	token, err := genAuthToken()
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`INSERT INTO auth_token (token_hash, login_key, created) VALUES (?, ?, ?)`,
		hashAuthToken(token), loginKey, time.Now())
	if err != nil {
		return "", err
	}
	return token, nil
}

func (db SQLiteDB) AuthTokenLoginKey(token string) (string, error) {
	var key string
	err := db.QueryRow("SELECT login_key FROM auth_token WHERE token_hash = ?", hashAuthToken(token)).Scan(&key)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return key, err
}

func (db SQLiteDB) RevokeAuthToken(token string) error {
	_, err := db.Exec("DELETE FROM auth_token WHERE token_hash = ?", hashAuthToken(token))
	return err
}

func (db SQLiteDB) RotateTempKey(loginKey string) (string, error) {
	for i := 0; i < tempKeyTries; i++ {
		key := genLoginKey()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM account WHERE login_key = ?", key).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			continue
		}
		res, err := db.Exec("UPDATE account SET temp_key = ? WHERE login_key = ?", key, loginKey)
		if err != nil {
			continue
		}
		if c, _ := res.RowsAffected(); c == 0 {
			return "", sql.ErrNoRows
		}
		return key, nil
	}
	return "", errors.New("no free temporary key")
}
