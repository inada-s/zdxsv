// Package dbtest gives tests of other packages a fresh SQLite DB.
package main

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

// UseTestDB points db.DefaultDB at a new SQLite DB in t.TempDir() until the test ends.
// The returned connection is for checks the DB interface does not cover.
func UseTestDB(t testing.TB) *sqlx.DB {
	t.Helper()
	conn, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "zdxsv_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	sdb := SQLiteDB{DB: conn, SQLiteCache: NewSQLiteCache()}
	if err := sdb.Init(); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	old := DefaultDB
	DefaultDB = sdb
	t.Cleanup(func() {
		DefaultDB = old
		conn.Close()
	})
	return conn
}
