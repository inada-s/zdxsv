package db

import (
	"database/sql"
	"flag"
	"log"
	"os"
	"reflect"
	"runtime"
	"testing"
)

var testDB DB
var testLoginKey string
var testUserId string

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

func Test001RegisterAccount(t *testing.T) {
	a, err := testDB.RegisterAccount("1.2.3.4")
	must(t, err)
	assertEq(t, "1.2.3.4", a.CreatedIP)
	assertEq(t, 10, len(a.LoginKey))
	testLoginKey = a.LoginKey
}

func Test002GetAccount(t *testing.T) {
	a, err := testDB.GetAccountByLoginKey(testLoginKey)
	must(t, err)
	assertEq(t, testLoginKey, a.LoginKey)
}

func Test002GetInvalidAccount(t *testing.T) {
	a, err := testDB.GetAccountByLoginKey("hogehoge01")
	if err == nil {
		t.FailNow()
	}
	if a != nil {
		t.FailNow()
	}
}

func Test101RegisterUser(t *testing.T) {
	u, err := testDB.RegisterUser(testLoginKey)
	must(t, err)
	if u == nil {
		t.FailNow()
	}
	assertEq(t, testLoginKey, u.LoginKey)
	assertEq(t, 6, len(u.UserId))
	assertEq(t, 0, u.BattleCount)
	assertEq(t, 0, u.WinCount)
	assertEq(t, 0, u.LoseCount)
	assertEq(t, 0, u.DailyBattleCount)
	assertEq(t, 0, u.DailyWinCount)
	assertEq(t, 0, u.DailyLoseCount)
	testUserId = u.UserId
}

func Test102GetUser(t *testing.T) {
	u, err := testDB.GetUser(testUserId)
	must(t, err)
	assertEq(t, testUserId, u.UserId)
	assertEq(t, 0, u.BattleCount)
	assertEq(t, 0, u.WinCount)
	assertEq(t, 0, u.LoseCount)
	assertEq(t, 0, u.DailyBattleCount)
	assertEq(t, 0, u.DailyWinCount)
	assertEq(t, 0, u.DailyLoseCount)
}

func Test103GetInvalidUser(t *testing.T) {
	u, err := testDB.GetUser("HOGE01")
	if err == nil {
		t.FailNow()
	}
	if u != nil {
		t.FailNow()
	}
}

func Test104UpdateUser(t *testing.T) {
	u, err := testDB.GetUser(testUserId)
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
	v, err := testDB.GetUser(testUserId)
	must(t, err)
	assertEq(t, u, v)
}

func Test105GetUserList(t *testing.T) {
	users, err := testDB.GetUserList(testLoginKey)
	must(t, err)
	assertEq(t, 1, len(users))
}

func Test106GetUserListNone(t *testing.T) {
	users, err := testDB.GetUserList("hogehoge01")
	must(t, err)
	assertEq(t, 0, len(users))
}

func Test200AddBattleRecord(t *testing.T) {
	br := &BattleRecord{
		BattleCode: "battlecode",
		UserId:     "123456",
		Players:    4,
		Pos:        1,
		Side:       2,
		System:     123,
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

func Test201AddUpdateBattleRecord(t *testing.T) {
	br := &BattleRecord{
		BattleCode: "battlecode",
		UserId:     "23456",
		Players:    4,
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

func TestMain(m *testing.M) {
	flag.Set("logtostderr", "true")
	flag.Parse()

	os.Remove("./zdxsv_test.db")
	conn, err := sql.Open("sqlite3", "./zdxsv_test.db")
	if err != nil {
		log.Fatalln("Cannot open test db. err:", err)
		os.Exit(1)
	}

	testDB = SQLiteDB{conn}
	err = testDB.Init()
	if err != nil {
		log.Fatalln("Faild to prepare DB. err:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}
