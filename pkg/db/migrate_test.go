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

func TestMigrateIdentityHoldsManyKeys(t *testing.T) {
	oldSchema := strings.Replace(schema, "PRIMARY KEY (login_key)\n);\nCREATE TABLE IF NOT EXISTS auth_token",
		"PRIMARY KEY (provider, provider_user_id)\n);\nCREATE TABLE IF NOT EXISTS auth_token", 1)
	oldIndexes := strings.Replace(indexes, "AUTH_IDENTITY_PROVIDER ON auth_identity(provider, provider_user_id)",
		"AUTH_IDENTITY_LOGIN_KEY ON auth_identity(login_key)", 1)
	if oldSchema == schema || oldIndexes == indexes {
		t.Fatal("old auth_identity schema not rebuilt")
	}
	db := openSchemaDB(t, oldSchema+oldIndexes)
	first, err := db.RegisterIdentityAccount("discord", "d1", "1.2.3.4")
	must(t, err)
	if _, err := db.Exec(`INSERT INTO auth_identity (provider, provider_user_id, login_key) VALUES ('discord', 'd1', 'x')`); err == nil {
		t.Fatal("old schema took a second key")
	}

	must(t, db.Migrate())
	assertIndexes(t, db)
	key, err := db.IdentityLoginKey("discord", "d1")
	must(t, err)
	assertEq(t, first.LoginKey, key)
	for i := 2; i <= MaxIdentityAccounts; i++ {
		_, err := db.RegisterIdentityAccount("discord", "d1", "1.2.3.4")
		must(t, err)
	}
	if _, err := db.RegisterIdentityAccount("discord", "d1", "1.2.3.4"); err != ErrIdentityFull {
		t.Fatal("account past the cap", err)
	}
	keys, err := identityKeys(db.DB, "discord", "d1")
	must(t, err)
	assertEq(t, MaxIdentityAccounts, len(keys))
	assertEq(t, first.LoginKey, keys[0])
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
	assertEq(t, []string{"ACCOUNT_LAST_LOGIN_MACHINE_ID", "AUTH_IDENTITY_PROVIDER", "AUTH_TOKEN_LOGIN_KEY", "BATTLE_RECORD_AGGRIGATE", "BATTLE_RECORD_CREATED", "BATTLE_RECORD_PLAYERS", "BATTLE_RECORD_USER_ID"}, names)
}
