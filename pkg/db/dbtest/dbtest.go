// Package dbtest gives tests of other packages a fresh SQLite DB.
package dbtest

import (
	"path/filepath"
	"testing"

	"zdxsv/pkg/db"

	"github.com/jmoiron/sqlx"
)

// Use points db.DefaultDB at a new SQLite DB in t.TempDir() until the test ends.
// The returned connection is for checks the DB interface does not cover.
func Use(t testing.TB) *sqlx.DB {
	t.Helper()
	conn, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "zdxsv_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	sdb := db.SQLiteDB{DB: conn, SQLiteCache: db.NewSQLiteCache()}
	if err := sdb.Init(); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	old := db.DefaultDB
	db.DefaultDB = sdb
	t.Cleanup(func() {
		db.DefaultDB = old
		conn.Close()
	})
	return conn
}
