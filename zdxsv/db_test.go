package main

import (
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var testDB DB
var testLoginKey string
var testUserID string

func must(tb testing.TB, err error) {
	if err != nil {
		pc, file, line, _ := runtime.Caller(1)
		name := runtime.FuncForPC(pc).Name()
		tb.Fatalf("In %s:%d %s\nerr:%vn", file, line, name, err)
	}
}

func assertEq(tb testing.TB, expected, actual interface{}) {
	ok := reflect.DeepEqual(expected, actual)
	if !ok {
		pc, file, line, _ := runtime.Caller(1)
		name := runtime.FuncForPC(pc).Name()
		tb.Fatalf("In %s:%d %s\nexpected: %#v \nactual: %#v\n", file, line, name, expected, actual)
	}
}

func test001RegisterAccount(t *testing.T) {
	a, err := testDB.RegisterAccount("1.2.3.4")
	must(t, err)
	assertEq(t, "1.2.3.4", a.CreatedIP)
	assertEq(t, 10, len(a.LoginKey))
	testLoginKey = a.LoginKey
}

func test002GetAccount(t *testing.T) {
	a, err := testDB.GetAccountByLoginKey(testLoginKey)
	must(t, err)
	assertEq(t, testLoginKey, a.LoginKey)
}

func test002GetInvalidAccount(t *testing.T) {
	a, err := testDB.GetAccountByLoginKey("hogehoge01")
	if err == nil {
		t.FailNow()
	}
	if a != nil {
		t.FailNow()
	}
}

func test101RegisterUser(t *testing.T) {
	u, err := testDB.RegisterUser(testLoginKey)
	must(t, err)
	if u == nil {
		t.FailNow()
	}
	assertEq(t, testLoginKey, u.LoginKey)
	assertEq(t, 6, len(u.UserID))
	assertEq(t, 0, u.BattleCount)
	assertEq(t, 0, u.WinCount)
	assertEq(t, 0, u.LoseCount)
	assertEq(t, 0, u.DailyBattleCount)
	assertEq(t, 0, u.DailyWinCount)
	assertEq(t, 0, u.DailyLoseCount)
	testUserID = u.UserID
}

func test102GetUser(t *testing.T) {
	u, err := testDB.GetUser(testUserID)
	must(t, err)
	assertEq(t, testUserID, u.UserID)
	assertEq(t, 0, u.BattleCount)
	assertEq(t, 0, u.WinCount)
	assertEq(t, 0, u.LoseCount)
	assertEq(t, 0, u.DailyBattleCount)
	assertEq(t, 0, u.DailyWinCount)
	assertEq(t, 0, u.DailyLoseCount)
}

func test103GetInvalidUser(t *testing.T) {
	_, err := testDB.GetUser("HOGE01")
	if err == nil {
		t.FailNow()
	}
}

func test104UpdateUser(t *testing.T) {
	u, err := testDB.GetUser(testUserID)
	must(t, err)
	u.Name = "テストユーザ"
	u.Team = "テストチーム"
	u.BattleCount = 100
	u.WinCount = 99
	u.LoseCount = 1
	u.DailyBattleCount = 10
	u.DailyWinCount = 9
	u.DailyLoseCount = 1
	err = testDB.UpdateUser(u)
	must(t, err)
	v, err := testDB.GetUser(testUserID)
	must(t, err)
	assertEq(t, u, v)
}

func test105GetUserList(t *testing.T) {
	users, err := testDB.GetUserList(testLoginKey)
	must(t, err)
	assertEq(t, 1, len(users))
}

func test106GetUserListNone(t *testing.T) {
	users, err := testDB.GetUserList("hogehoge01")
	must(t, err)
	assertEq(t, 0, len(users))
}

func test200AddBattleRecord(t *testing.T) {
	br := &BattleRecord{
		BattleCode: "battlecode",
		UserID:     "123456",
		Players:    4,
		Pos:        1,
		Side:       2,
		System:     123,
		Game:       gameCodeAEUG,
	}
	err := testDB.AddBattleRecord(br)
	must(t, err)

	actual, err := testDB.GetBattleRecordUser(br.BattleCode, "123456")
	must(t, err)

	// These values are automatically set.
	br.Created = actual.Created
	br.Updated = actual.Updated
	assertEq(t, br, actual)
}

func test201AddUpdateBattleRecord(t *testing.T) {
	br := &BattleRecord{
		BattleCode: "battlecode",
		UserID:     "23456",
		Players:    4,
		Aggregate:  1,
		Pos:        1,
		Round:      10,
		Win:        7,
		Lose:       3,
		Kill:       123,
		Death:      456,
		Frame:      9999,
		Result:     "result",
		Side:       2,
		System:     123,
	}
	err := testDB.AddBattleRecord(br)
	must(t, err)
	err = testDB.UpdateBattleRecord(br)
	must(t, err)

	actual, err := testDB.GetBattleRecordUser(br.BattleCode, "23456")

	// These values are automatically set.
	br.Created = actual.Created
	br.Updated = actual.Updated
	assertEq(t, br, actual)
}

func test202SetReplayURL(t *testing.T) {
	for _, u := range []string{"r1", "r2"} {
		must(t, testDB.AddBattleRecord(&BattleRecord{BattleCode: "replaycode", UserID: u, Players: 2}))
	}
	must(t, testDB.AddBattleRecord(&BattleRecord{BattleCode: "othercode", UserID: "r1", Players: 2}))
	must(t, testDB.SetReplayURL("replaycode", "https://example.com/replaycode.pb"))

	for _, u := range []string{"r1", "r2"} {
		actual, err := testDB.GetBattleRecordUser("replaycode", u)
		must(t, err)
		assertEq(t, "https://example.com/replaycode.pb", actual.ReplayURL)
	}
	other, err := testDB.GetBattleRecordUser("othercode", "r1")
	must(t, err)
	assertEq(t, "", other.ReplayURL)
}

func test204FindReplay(t *testing.T) {
	// after Test202: replaycode (r1, r2) has a replay, othercode none
	must(t, testDB.AddBattleRecord(&BattleRecord{BattleCode: "replaycode2", UserID: "r4", PilotName: "zaku", Players: 2, Pos: 2, Side: 2}))
	must(t, testDB.AddBattleRecord(&BattleRecord{BattleCode: "replaycode2", UserID: "r3", PilotName: "gundam", Players: 2, Pos: 1, Side: 1}))
	must(t, testDB.SetReplayURL("replaycode2", "https://example.com/replaycode2.pb"))
	_, err := testDB.(SQLiteDB).Exec(`UPDATE battle_record SET created = ? WHERE battle_code = "replaycode2"`, time.Now().Add(time.Second))
	must(t, err)

	codes := func(q *FindReplayQuery) []string {
		found, err := testDB.FindReplay(q)
		must(t, err)
		var ret []string
		for _, f := range found {
			ret = append(ret, f.BattleCode)
		}
		return ret
	}
	assertEq(t, []string{"replaycode2", "replaycode"}, codes(NewFindReplayQuery()))
	q := NewFindReplayQuery()
	q.Reverse = true
	assertEq(t, []string{"replaycode", "replaycode2"}, codes(q))
	q = NewFindReplayQuery()
	q.UserIDs = []string{"r3", "nobody"}
	assertEq(t, []string{"replaycode2"}, codes(q))
	q = NewFindReplayQuery()
	q.PilotNames = []string{"gun%"}
	assertEq(t, []string{"replaycode2"}, codes(q))
	q = NewFindReplayQuery()
	q.BattleCode = "othercode"
	assertEq(t, []string(nil), codes(q))
	q = NewFindReplayQuery()
	q.Page = 1
	assertEq(t, []string(nil), codes(q))

	q = NewFindReplayQuery()
	q.BattleCode = "replaycode2"
	found, err := testDB.FindReplay(q)
	must(t, err)
	assertEq(t, 1, len(found))
	assertEq(t, "https://example.com/replaycode2.pb", found[0].ReplayURL)
	assertEq(t, 2, len(found[0].Users))
	assertEq(t, "r3", found[0].Users[0].UserID)
	assertEq(t, 1, found[0].Users[0].Team)
	assertEq(t, "r4", found[0].Users[1].UserID)
}

func test203CalculateUserBattleCount(t *testing.T) {
	br := &BattleRecord{
		BattleCode: "battlecode",
		UserID:     "11111",
		Players:    4,
		Aggregate:  1,
		Pos:        1,
		Round:      10,
		Win:        7,
		Lose:       3,
		Kill:       123,
		Death:      456,
		Frame:      9999,
		Result:     "result",
		Side:       2,
		System:     123,
	}

	err := testDB.AddBattleRecord(br)
	must(t, err)
	err = testDB.UpdateBattleRecord(br)
	must(t, err)

	rec, err := testDB.CalculateUserTotalBattleCount("11111", 0)
	must(t, err)

	assertEq(t, br.Round, rec.Battle)
	assertEq(t, br.Win, rec.Win)
	assertEq(t, br.Lose, rec.Lose)
	assertEq(t, br.Kill, rec.Kill)
	assertEq(t, br.Death, rec.Death)

	rec, err = testDB.CalculateUserDailyBattleCount("11111")
	must(t, err)

	assertEq(t, br.Round, rec.Battle)
	assertEq(t, br.Win, rec.Win)
	assertEq(t, br.Lose, rec.Lose)
	assertEq(t, br.Kill, rec.Kill)
	assertEq(t, br.Death, rec.Death)
	assertEq(t, br.Round, rec.Battle)
}

func test300Ranking(t *testing.T) {
	testDB.(SQLiteDB).Exec("DELETE FROM user")

	var users []*DBUser
	for i := 0; i < 3; i++ {
		ac, err := testDB.RegisterAccount("12.34.56.78")
		must(t, err)
		u, err := testDB.RegisterUser(ac.LoginKey)
		must(t, err)
		users = append(users, u)
	}

	for _, br := range []*BattleRecord{
		&BattleRecord{
			BattleCode: "rankingtest0",
			UserID:     users[0].UserID,
			Players:    4,
			Aggregate:  1,
			Pos:        1,
			Round:      1000,
			Win:        1000,
			Lose:       0,
			Kill:       1000,
			Death:      456,
			Frame:      9999,
			Result:     "result",
			Side:       1,
			System:     123,
		},
		&BattleRecord{
			BattleCode: "rankingtest1",
			UserID:     users[1].UserID,
			Players:    4,
			Aggregate:  1,
			Pos:        2,
			Round:      1000,
			Win:        1000,
			Lose:       0,
			Kill:       1000,
			Death:      456,
			Frame:      9999,
			Result:     "result",
			Side:       2,
			System:     123,
		},
		&BattleRecord{
			BattleCode: "rankingtest2",
			UserID:     users[2].UserID,
			Players:    4,
			Aggregate:  1,
			Pos:        2,
			Round:      10,
			Win:        5,
			Lose:       5,
			Kill:       100,
			Death:      100,
			Frame:      9999,
			Result:     "result",
			Side:       2,
			System:     123,
		},
	} {
		err := testDB.AddBattleRecord(br)
		must(t, err)
		err = testDB.UpdateBattleRecord(br)
		must(t, err)
	}

	for _, u := range users {
		rec, err := testDB.CalculateUserTotalBattleCount(u.UserID, 0)
		must(t, err)
		u.BattleCount = rec.Battle
		u.WinCount = rec.Win
		u.LoseCount = rec.Lose
		u.KillCount = rec.Kill
		u.DeathCount = rec.Death

		rec, err = testDB.CalculateUserTotalBattleCount(u.UserID, 1)
		must(t, err)
		u.AeugBattleCount = rec.Battle
		u.AeugWinCount = rec.Win
		u.AeugLoseCount = rec.Lose
		u.AeugKillCount = rec.Kill
		u.AeugDeathCount = rec.Death

		rec, err = testDB.CalculateUserTotalBattleCount(u.UserID, 2)
		must(t, err)
		u.TitansBattleCount = rec.Battle
		u.TitansWinCount = rec.Win
		u.TitansLoseCount = rec.Lose
		u.TitansKillCount = rec.Kill
		u.TitansDeathCount = rec.Death

		err = testDB.UpdateUser(u)
		t.Log(*u)
		must(t, err)
	}

	totalRanking, err := testDB.GetWinCountRanking(0)
	must(t, err)

	assertEq(t, 1000, totalRanking[0].WinCount)
	assertEq(t, 1000, totalRanking[0].BattleCount)
	assertEq(t, 1, totalRanking[0].Rank)
	assertEq(t, 1, totalRanking[1].Rank)
	assertEq(t, 3, totalRanking[2].Rank)

	aeugRanking, err := testDB.GetWinCountRanking(1)
	must(t, err)

	assertEq(t, 1000, aeugRanking[0].AeugWinCount)
	assertEq(t, 1000, aeugRanking[0].BattleCount)
	assertEq(t, 0, aeugRanking[1].AeugWinCount)

	assertEq(t, 1, aeugRanking[0].Rank)
	assertEq(t, 2, aeugRanking[1].Rank)
	assertEq(t, 2, aeugRanking[2].Rank)

	assertEq(t, 1000, aeugRanking[0].WinCount)

	titansRanking, err := testDB.GetWinCountRanking(2)
	must(t, err)

	assertEq(t, 1000, titansRanking[0].TitansWinCount)
	assertEq(t, 1000, titansRanking[0].BattleCount)
	assertEq(t, 5, titansRanking[1].TitansWinCount)

	assertEq(t, 1, titansRanking[0].Rank)
	assertEq(t, 2, titansRanking[1].Rank)
	assertEq(t, 3, titansRanking[2].Rank)
}

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

func newTestDB(t *testing.T) DB {
	conn, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "zdxsv_test.db"))
	must(t, err)
	t.Cleanup(func() { conn.Close() })
	db := SQLiteDB{
		DB:          conn,
		SQLiteCache: NewSQLiteCache(),
	}
	must(t, db.Init())
	return db
}

// The steps share one DB and run in order: later steps read what earlier ones wrote.
func TestSQLiteDB(t *testing.T) {
	testDB = newTestDB(t)
	steps := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"RegisterAccount", test001RegisterAccount},
		{"GetAccount", test002GetAccount},
		{"GetInvalidAccount", test002GetInvalidAccount},
		{"RegisterUser", test101RegisterUser},
		{"GetUser", test102GetUser},
		{"GetInvalidUser", test103GetInvalidUser},
		{"UpdateUser", test104UpdateUser},
		{"GetUserList", test105GetUserList},
		{"GetUserListNone", test106GetUserListNone},
		{"AddBattleRecord", test200AddBattleRecord},
		{"AddUpdateBattleRecord", test201AddUpdateBattleRecord},
		{"SetReplayURL", test202SetReplayURL},
		{"FindReplay", test204FindReplay},
		{"CalculateUserBattleCount", test203CalculateUserBattleCount},
		{"Ranking", test300Ranking},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.fn) {
			t.FailNow()
		}
	}
}

func TestGeneratedKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		gen  func() string
		re   *regexp.Regexp
	}{
		{"login key", GenLoginKey, regexp.MustCompile(`^[a-zA-Z0-9]{10}$`)},
		{"user id", genUserID, regexp.MustCompile(`^[A-Z2-9]{6}$`)},
		{"session id", GenSessionID, regexp.MustCompile(`^[1-9]{8}$`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := map[string]bool{}
			for i := 0; i < 1000; i++ {
				k := tc.gen()
				if !tc.re.MatchString(k) {
					t.Fatalf("%q does not match %v", k, tc.re)
				}
				seen[k] = true
			}
			if len(seen) < 990 {
				t.Errorf("%d distinct of 1000", len(seen))
			}

			// Keys are credentials: they must not follow the math/rand seed.
			rand.Seed(1)
			a := tc.gen()
			rand.Seed(1)
			if b := tc.gen(); a == b {
				t.Errorf("same key %q after the same math/rand seed", a)
			}
		})
	}
}

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

func TestMigrateAddsGame(t *testing.T) {
	before := strings.Replace(schema, "\tgame        text default '00000020',\n", "", 1)
	if before == schema {
		t.Fatal("game column not found in the schema")
	}
	db := openSchemaDB(t, before+indexes)
	_, err := db.Exec(`INSERT INTO battle_record (battle_code, user_id, players) VALUES ('b1', 'U1', 2)`)
	must(t, err)
	must(t, db.Migrate())
	var game string
	must(t, db.Get(&game, `SELECT game FROM battle_record WHERE battle_code = 'b1'`))
	assertEq(t, gameCodeZGVSZ, game)
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
	for i := 2; i <= MaxIdentityNewAccounts; i++ {
		_, err := db.RegisterIdentityAccount("discord", "d1", "1.2.3.4")
		must(t, err)
	}
	if _, err := db.RegisterIdentityAccount("discord", "d1", "1.2.3.4"); err != ErrIdentityFull {
		t.Fatal("new account past the cap", err)
	}
	for i := 1; i <= MaxIdentityCarriedAccounts; i++ {
		a, err := db.RegisterAccount("1.2.3.4")
		must(t, err)
		must(t, db.LinkIdentity("discord", "d1", a.LoginKey))
	}
	keys, err := identityKeys(db.DB, "discord", "d1")
	must(t, err)
	assertEq(t, MaxIdentityNewAccounts+MaxIdentityCarriedAccounts, len(keys))
	assertEq(t, first.LoginKey, keys[0])
}

func TestMigrateDropsBan(t *testing.T) {
	db := openSchemaDB(t, schema+indexes+`CREATE TABLE ban (kind text, value text, reason text default '', created timestamp, PRIMARY KEY (kind, value));
INSERT INTO ban (kind, value) VALUES ('ip', '1.2.3.4');`)
	must(t, db.Migrate())
	assertIndexes(t, db)
	var n int
	must(t, db.Get(&n, `SELECT count(*) FROM sqlite_master WHERE name = 'ban'`))
	assertEq(t, 0, n)
}

func TestMigrateEmptyDB(t *testing.T) {
	db := openSchemaDB(t, schemaBeforeReplayURL+indexes)
	must(t, db.Migrate())
	assertIndexes(t, db)
	must(t, db.SetReplayURL("none", "http://r/1"))
}

func TestMigrateDropsAccountSessionColumns(t *testing.T) {
	oldSchema := strings.Replace(schema, "\tsystem integer default 0,\n\tPRIMARY KEY (login_key)\n);\nCREATE TABLE IF NOT EXISTS auth_identity",
		"\tsystem integer default 0,\n\tsession_id text default '',\n\ttemp_key text UNIQUE,\n\tPRIMARY KEY (login_key)\n);\nCREATE TABLE IF NOT EXISTS auth_identity", 1)
	if oldSchema == schema {
		t.Fatal("old account schema not rebuilt")
	}
	db := openSchemaDB(t, oldSchema+indexes)
	_, err := db.Exec(`INSERT INTO account (login_key, session_id, temp_key, last_user_id, created_ip, created, last_login) VALUES ('key1', '12345678', 'temp1', 'USER01', '1.2.3.4', ?, ?)`,
		time.Now(), time.Now())
	must(t, err)

	must(t, db.Migrate())
	assertIndexes(t, db)

	ac, err := db.GetAccountByLoginKey("key1")
	must(t, err)
	assertEq(t, [3]string{"key1", "USER01", "1.2.3.4"}, [3]string{ac.LoginKey, ac.LastUserID, ac.CreatedIP})
	var dropped []string
	must(t, db.Select(&dropped, `SELECT name FROM pragma_table_info('account') WHERE name IN ('session_id', 'temp_key')`))
	assertEq(t, 0, len(dropped))
}

// assertIndexes checks the indexes are there and no table copy is left.
func assertIndexes(t *testing.T, db SQLiteDB) {
	t.Helper()
	var names []string
	must(t, db.Select(&names, `SELECT name FROM sqlite_master WHERE name LIKE '%_tmp' OR type = 'index' AND name NOT LIKE 'sqlite_%' ORDER BY name`))
	assertEq(t, []string{"ACCOUNT_LAST_LOGIN_MACHINE_ID", "AUTH_IDENTITY_PROVIDER", "AUTH_TOKEN_LOGIN_KEY", "BATTLE_RECORD_AGGRIGATE", "BATTLE_RECORD_CREATED", "BATTLE_RECORD_PLAYERS", "BATTLE_RECORD_USER_ID"}, names)
}

// UseTestDB points DefaultDB at a new SQLite DB in t.TempDir() until the test ends.
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
