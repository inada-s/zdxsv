package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// schemaBeforeReplayURL is what initdb created before
// battle_record.replay_url existed.
var schemaBeforeReplayURL = strings.Replace(schema, "\treplay_url  text default '',\n", "", 1)

func openSchemaDB(t *testing.T, ddl string) SQLiteDB {
	t.Helper()
	conn, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "zdxsv_test.db"))
	must(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Exec(ddl)
	must(t, err)
	return SQLiteDB{DB: conn, SQLiteCache: NewSQLiteCache()}
}

func TestMigrateAddsReplayURL(t *testing.T) {
	if schemaBeforeReplayURL == schema {
		t.Fatal("replay_url column not found in the schema")
	}
	db := openSchemaDB(t, schemaBeforeReplayURL+indexes)

	ac, err := db.RegisterAccount("1.2.3.4")
	must(t, err)
	u, err := db.RegisterUser(ac.LoginKey)
	must(t, err)
	u.Name, u.Team = "pilot", "team"
	u.BattleCount, u.WinCount, u.KillCount, u.AeugWinCount = 9, 5, 21, 3
	must(t, db.UpdateUser(u))
	rec := &BattleRecord{BattleCode: "b1", UserID: u.UserID, UserName: "pilot", PilotName: "p", Players: 4, Aggregate: 1, Pos: 2, Side: 1}
	must(t, db.AddBattleRecord(rec))
	rec.Round, rec.Win, rec.Lose, rec.Kill, rec.Death, rec.Frame = 5, 3, 2, 7, 4, 12345
	must(t, db.UpdateBattleRecord(rec))
	if err := db.SetReplayURL("b1", "http://r/1"); err == nil {
		t.Fatal("SetReplayURL works before the migration")
	}

	must(t, db.Migrate())
	assertIndexes(t, db)
	must(t, db.Migrate()) // a second run on the current schema keeps the data
	assertIndexes(t, db)

	gotAc, err := db.GetAccountByLoginKey(ac.LoginKey)
	must(t, err)
	assertEq(t, "1.2.3.4", gotAc.CreatedIP)
	gotU, err := db.GetUser(u.UserID)
	must(t, err)
	assertEq(t, [6]interface{}{"pilot", "team", 9, 5, 21, 3},
		[6]interface{}{gotU.Name, gotU.Team, gotU.BattleCount, gotU.WinCount, gotU.KillCount, gotU.AeugWinCount})
	got, err := db.GetBattleRecordUser("b1", u.UserID)
	must(t, err)
	assertEq(t, [11]interface{}{"pilot", "p", 4, 1, 2, 1, 5, 3, 2, 7, 12345},
		[11]interface{}{got.UserName, got.PilotName, got.Players, got.Aggregate, got.Pos, got.Side, got.Round, got.Win, got.Lose, got.Kill, got.Frame})
	assertEq(t, "", got.ReplayURL)

	must(t, db.SetReplayURL("b1", "http://r/1"))
	found, err := db.FindReplay(&FindReplayQuery{BattleCode: "b1", Players: -1, Aggregate: -1})
	must(t, err)
	if len(found) != 1 || found[0].ReplayURL != "http://r/1" || found[0].Round != 5 {
		t.Errorf("FindReplay after the migration: %+v", found)
	}
}

func TestMigrateEmptyDB(t *testing.T) {
	db := openSchemaDB(t, schemaBeforeReplayURL+indexes)
	must(t, db.Migrate())
	assertIndexes(t, db)
	must(t, db.SetReplayURL("none", "http://r/1"))
}

// assertIndexes checks the indexes are there and no table copy is left.
func assertIndexes(t *testing.T, db SQLiteDB) {
	t.Helper()
	var names []string
	must(t, db.Select(&names, `SELECT name FROM sqlite_master WHERE name LIKE '%_tmp' OR type = 'index' AND name NOT LIKE 'sqlite_%' ORDER BY name`))
	assertEq(t, []string{"BATTLE_RECORD_AGGRIGATE", "BATTLE_RECORD_CREATED", "BATTLE_RECORD_PLAYERS", "BATTLE_RECORD_USER_ID"}, names)
}
