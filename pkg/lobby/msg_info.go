package lobby

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

var reTwoNumber = regexp.MustCompile(`\d{2}`)

func rankingPage(r []*db.RankingRecord, page int, size int) ([]*db.RankingRecord, int) {
	lb := page * size
	if lb > len(r) {
		lb = len(r)
	}

	ub := lb + size
	if ub > len(r) {
		ub = len(r)
	}

	maxPage := 0
	if 0 < len(r) {
		n := float64(len(r))
		maxPage = int(math.Ceil(n/float64(size))) - 1
	}
	return r[lb:ub], maxPage
}

var _ = register(0x6801, "GetRegurationData", func(p *AppPeer, m *Message) {
	path := m.Reader().ReadEncryptedString()
	buf := new(bytes.Buffer)
	tw := &runeWriter{transform.NewWriter(buf, japanese.ShiftJIS.NewEncoder())}
	// 06/INFOR/INFOR00.HTM   : インフォメーション
	// 06/V_RANK/TOTAL00.HTM  : 通信対戦ランキング > 総合
	// 06/V_RANK/AEUG00.HTM   : 通信対戦ランキング > 連邦エゥーゴ
	// 06/V_RANK/TITANS00.HTM : 通信対戦ランキング > ジオンティターンズ
	// 06/P_RANK/TOTAL00.HTM  : 撃墜数ランキング > 総合
	// 06/P_RANK/AEUG00.HTM   : 撃墜数ランキング > 連邦エゥーゴ
	// 06/P_RANK/TITANS00.HTM : 撃墜数ランキング > ジオンティターンズ
	// 06/E_RANK/TOTAL00.HTM  : イベントランキング > 総合
	// 06/E_RANK/AEUG00.HTM   : イベントランキング > 連邦エゥーゴ
	// 06/E_RANK/TITANS00.HTM : イベントランキング > ジオンティターンズ

	type rankingParamRecord struct {
		Rank   int
		UserID string
		Name   string
		Team   string
		Score  string
	}

	type rankingParams struct {
		Title    string
		Records  []rankingParamRecord
		HasPrev  bool
		HasNext  bool
		PrevLink string
		NextLink string
	}

	// detect side and page number.
	page := 0
	nums := reTwoNumber.FindAllString(path, 2)
	if 2 <= len(nums) {
		page, _ = strconv.Atoi(nums[1])
	}
	side := byte(0)
	if strings.Contains(path, "AEUG") {
		side = 1
	} else if strings.Contains(path, "TITANS") {
		side = 2
	}

	switch {
	case strings.Contains(path, "V_RANK"): //通信対戦ランキング
		ranking, err := db.DefaultDB.GetWinCountRanking(side)
		users, maxPage := rankingPage(ranking, page, 10)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{
			HasPrev: 0 < page,
			HasNext: page < maxPage,
		}
		switch side {
		case 0:
			rp.Title = "勝利数ランキング(総合)"
			rp.NextLink = fmt.Sprintf("TOTAL%02d", page+1)
			rp.PrevLink = fmt.Sprintf("TOTAL%02d", page-1)
			for _, u := range users {
				rp.Records = append(rp.Records, rankingParamRecord{
					Rank:   u.Rank,
					UserID: u.UserID,
					Name:   u.Name,
					Team:   u.Team,
					Score: fmt.Sprintf("%5d戦 %5d勝 %5d敗 (無効: %5d)",
						u.BattleCount, u.WinCount, u.LoseCount,
						u.BattleCount-u.WinCount-u.LoseCount),
				})
			}
		case 1:
			rp.Title = "勝利数ランキング(連邦・エゥーゴ)"
			rp.NextLink = fmt.Sprintf("AEUG%02d", page+1)
			rp.PrevLink = fmt.Sprintf("AEUG%02d", page-1)
			for _, u := range users {
				rp.Records = append(rp.Records, rankingParamRecord{
					Rank:   u.Rank,
					UserID: u.UserID,
					Name:   u.Name,
					Team:   u.Team,
					Score: fmt.Sprintf("%5d戦 %5d勝 %5d敗 (無効: %5d)",
						u.AeugBattleCount, u.AeugWinCount, u.AeugLoseCount,
						u.AeugBattleCount-u.AeugWinCount-u.AeugLoseCount),
				})
			}
		case 2:
			rp.Title = "勝利数ランキング(ジオン・ティターンズ)"
			rp.NextLink = fmt.Sprintf("TITANS%02d", page+1)
			rp.PrevLink = fmt.Sprintf("TITANS%02d", page-1)
			for _, u := range users {
				rp.Records = append(rp.Records, rankingParamRecord{
					Rank:   u.Rank,
					UserID: u.UserID,
					Name:   u.Name,
					Team:   u.Team,
					Score: fmt.Sprintf("%5d戦 %5d勝 %5d敗 (無効: %5d)",
						u.TitansBattleCount, u.TitansWinCount, u.TitansLoseCount,
						u.TitansBattleCount-u.TitansWinCount-u.TitansLoseCount),
				})
			}
		}
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	case strings.Contains(path, "P_RANK"): //撃墜数ランキング
		ranking, err := db.DefaultDB.GetKillCountRanking(side)
		if err != nil {
			glog.Errorln(err)
		}
		users, maxPage := rankingPage(ranking, page, 10)
		rp := rankingParams{
			HasPrev: 0 < page,
			HasNext: page < maxPage,
		}
		switch side {
		case 0:
			rp.Title = "撃墜数ランキング(総合)"
			rp.NextLink = fmt.Sprintf("TOTAL%02d", page+1)
			rp.PrevLink = fmt.Sprintf("TOTAL%02d", page-1)
		case 1:
			rp.Title = "撃墜数ランキング(連邦・エゥーゴ)"
			rp.NextLink = fmt.Sprintf("AEUG%02d", page+1)
			rp.PrevLink = fmt.Sprintf("AEUG%02d", page-1)
		case 2:
			rp.Title = "撃墜数ランキング(ジオン・ティターンズ)"
			rp.NextLink = fmt.Sprintf("TITANS%02d", page+1)
			rp.PrevLink = fmt.Sprintf("TITANS%02d", page-1)
		}
		for _, u := range users {
			rp.Records = append(rp.Records, rankingParamRecord{
				Rank:   u.Rank,
				UserID: u.UserID,
				Name:   u.Name,
				Team:   u.Team,
				Score:  fmt.Sprintf("撃墜数：%d 機", u.KillCount),
			})
		}
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	default:
		glog.Errorln("page request", path)
		err := tplUnderConstruction.Execute(tw, nil)
		if err != nil {
			glog.Errorln(err)
		}
	}

	// FIXME: <GAME-STYLE> tag doesn't work.
	// FIXME: invalid string may appear in team name.
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write16(uint16(len(path)))
	w.Write([]byte(path))
	w.Write16(uint16(buf.Len()))
	w.Write(buf.Bytes())
	p.SendMessage(a)
})

// workaround to ignore invalid string
// c.f. https://teratail.com/questions/106106
type runeWriter struct {
	w io.Writer
}

func (rw *runeWriter) Write(b []byte) (int, error) {
	var err error
	l := 0

loop:
	for len(b) > 0 {
		_, n := utf8.DecodeRune(b)
		if n == 0 {
			break loop
		}
		rw.w.Write(b[:n])
		l += n
		b = b[n:]
	}
	return l, err
}

var tplUnderConstruction = template.Must(template.New("unc").Parse(`
<HTML>
<HEAD>
	<TITLE> UNDER CONSTRUCTION </TITLE>
	<meta http-equiv="Content-Type" content="text/html; charset=Shift_JIS">
<!--
	<GAME-STYLE>
		"MOUSE=OFF",
		"SCROLL=OFF",
		"TITLE=OFF",
		"BACK=ON:afs://02/8",
		"FORWARD=OFF",
		"CANCEL=OFF",
		"RELOAD=OFF",
		"CHOICE_MV=OFF",
		"X_SHOW=ON",
		"LINK_U=OFF",
	</GAME-STYLE>
-->
</HEAD>
<BODY BGCOLOR=#000000 background=afs://02/114.PNG text=white link=white vlink=white>
<TABLE WIDTH=584 CELLSPACING=0 CELLPADDING=0>

<!-- タイトル -->
	<TR>
	<TD BACKGROUND=afs://02/121.PNG WIDTH=256 HEIGHT=44>
	<TD BACKGROUND=afs://02/122.PNG WIDTH=32  HEIGHT=44>
	<TD BACKGROUND=afs://02/123.PNG WIDTH=296 HEIGHT=44 ALIGN=RIGHT><font size=1>　<br></font>UNDER CONSTRUCTION
	</TR>

	<TR><TD COLSPAN=3>

<!-- 項目 -->
<CENTER>
</CENTER>

</BODY>
</HTML>
`))

var tplRanking = template.Must(template.New("ranking").Parse(`
<HTML>
<HEAD><TITLE>{{.Title}}</TITLE>
<meta http-equiv="Content-Type" content="text/html; charset=Shift_JIS">
</HEAD>
<BODY BGCOLOR=#000000 background=afs://02/114.PNG text=white link=white vlink=white>

<TABLE WIDTH=584 CELLSPACING=0 CELLPADDING=0>
<!-- タイトル -->
<TR>
<TD BACKGROUND=afs://02/121.PNG WIDTH=256 HEIGHT=44>
<TD BACKGROUND=afs://02/122.PNG WIDTH=32 HEIGHT=44>
<TD BACKGROUND=afs://02/123.PNG WIDTH=296 HEIGHT=44>
</TR>

<TR>
<TD COLSPAN=3>

<!-- 項目 -->
<CENTER>
<FONT SIZE=3>
<TABLE WIDTH=530 CELLSPACING=0 CELLPADDING=0>
<TR>
<TD COLSPAN=6 HEIGHT=10>

{{range .Records}}
<TR>
<TD BGCOLOR=#ff7500 COLSPAN=6 HEIGHT=2>
<TR>
<TD BGCOLOR=#ff7500 WIDTH=5 HEIGHT=30>
<TD BGCOLOR=#000000 ROWSPAN=2 WIDTH=60 ALIGN=CENTER>{{.Rank}}位
<TD BGCOLOR=#000000 ROWSPAN=2 WIDTH=60 ALIGN=CENTER><FONT COLOR=#FFA100>{{.UserID}}</FONT>
<TD BGCOLOR=#000000 WIDTH=200 ALIGN=CENTER>{{.Name}}
<TD BGCOLOR=#000000 WIDTH=200 ALIGN=CENTER>{{.Team}}
<TD BGCOLOR=#ff7500 WIDTH=5>
<TR>
<TD BGCOLOR=#ff7500 WIDTH=5 HEIGHT=30>
<TD BGCOLOR=#000000 WIDTH=400 COLSPAN=2 ALIGN=CENTER>{{.Score}}
<TD BGCOLOR=#ff7500 WIDTH=5>
<TR>
<TD BGCOLOR=#ff7500 COLSPAN=6 HEIGHT=2>
{{end}}

</TABLE>
</FONT>
<TABLE BORDER="0" CELLSPACING="0" CELLPADDING="0">
<TR>
{{ if .HasPrev }}
<TD WIDTH=275 align=right><a href="{{.PrevLink}}"><IMG SRC="afs://02/102.PNG" width="221" height="30" BORDER="0"></a>
{{ else }}
<TD WIDTH=275 align=right><IMG SRC="afs://02/104.PNG" width="221" height="30" BORDER="0"></a>
{{ end }}
<TD WIDTH=34>
{{ if .HasNext }}
<TD WIDTH=275 align=left ><a href="{{.NextLink}}"><IMG SRC="afs://02/103.PNG" width="221" height="30" BORDER="0"></a>
{{ else }}
<TD WIDTH=275 align=left ><IMG SRC="afs://02/105.PNG" width="221" height="30" BORDER="0"></a>
{{ end }}
</TR>
</TABLE>
</CENTER>
</BODY>
</HTML>
`))
