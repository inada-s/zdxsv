package lobby

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
)

var reRankingRow = regexp.MustCompile(`(\d+)位\s*<TD[^>]*><FONT COLOR=#FFA100>(\w+)</FONT>`)

// rankingPage asks for an information page and returns its HTML and the
// "<rank>:<user ID>" of each row.
func (c *testClient) rankingPage(path string) (string, []string) {
	c.t.Helper()
	q := NewClientQuestion(0x6801)
	q.Writer().Write(encryptString(q.Seq, path))
	c.send(q)
	r := c.expect(0x6801, CategoryAnswer).Reader()
	if got := r.ReadString(); got != path {
		c.t.Fatalf("answer path %q, want %q", got, path)
	}
	html, err := japanese.ShiftJIS.NewDecoder().String(r.ReadString())
	if err != nil {
		c.t.Fatal(err)
	}
	var rows []string
	for _, m := range reRankingRow.FindAllStringSubmatch(html, -1) {
		rows = append(rows, m[1]+":"+m[2])
	}
	return html, rows
}

func TestLobbyRankingPages(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())

	// 12 ranked users: user i has i+1 wins, 12-i Aeug wins and 2i kills.
	var ids []string
	for i := 0; i < 12; i++ {
		u, err := db.DefaultDB.RegisterUser(fmt.Sprint("rank", i))
		if err != nil {
			t.Fatal(err)
		}
		u.Name = fmt.Sprint("pilot", i)
		u.BattleCount, u.WinCount, u.LoseCount = 20, i+1, 3
		u.AeugBattleCount, u.AeugWinCount, u.AeugLoseCount = 20, 12-i, 1
		u.KillCount = 2 * i
		if err := db.DefaultDB.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.UserID)
	}
	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "viewer") // 13th user, all counts 0

	rows := func(rank int, users ...int) []string {
		var r []string
		for _, i := range users {
			r = append(r, fmt.Sprint(rank, ":", ids[i]))
			rank++
		}
		return r
	}
	// rows lists the first rows of a page; n is its row count. Ties share a
	// rank in any order: all 13 Titans kill counts are 0, user 0 and the
	// viewer have 0 kills.
	cases := []struct {
		path       string
		rows       []string
		n          int
		prev, next string // link targets, "" = none
		text       string
	}{
		{"06/V_RANK/TOTAL00.HTM", rows(1, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2), 10, "", "TOTAL01", "   20戦    12勝     3敗 (無効:     5)"},
		{"06/V_RANK/TOTAL01.HTM", rows(11, 1, 0), 3, "TOTAL00", "", "勝利数ランキング(総合)"},
		{"06/V_RANK/TOTAL05.HTM", nil, 0, "TOTAL04", "", ""},
		{"06/V_RANK/AEUG00.HTM", rows(1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9), 10, "", "AEUG01", "   20戦    12勝     1敗 (無効:     7)"},
		{"06/P_RANK/TITANS00.HTM", nil, 10, "", "TITANS01", "撃墜数ランキング(ジオン・ティターンズ)"},
		{"06/P_RANK/TOTAL01.HTM", rows(11, 1), 3, "TOTAL00", "", "撃墜数：2 機"},
	}
	for _, tc := range cases {
		html, got := c.rankingPage(tc.path)
		if len(got) != tc.n || fmt.Sprint(got[:len(tc.rows)]) != fmt.Sprint(tc.rows) {
			t.Errorf("%s rows %v\nwant %d rows starting %v", tc.path, got, tc.n, tc.rows)
		}
		links := 0
		for _, to := range []string{tc.prev, tc.next} {
			if to == "" {
				continue
			}
			links++
			if !strings.Contains(html, `<a href="`+to+`">`) {
				t.Errorf("%s: no link to %s", tc.path, to)
			}
		}
		if n := strings.Count(html, `<a href="`); n != links {
			t.Errorf("%s: %d links, want %d", tc.path, n, links)
		}
		if !strings.Contains(html, tc.text) {
			t.Errorf("%s does not contain %q", tc.path, tc.text)
		}
	}

	if html, _ := c.rankingPage("06/INFOR/INFOR00.HTM"); strings.Contains(html, "位") {
		t.Errorf("information page lists a ranking")
	}
}
