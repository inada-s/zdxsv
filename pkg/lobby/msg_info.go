package lobby

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"unicode/utf8"
	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

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
		Title   string
		Records []rankingParamRecord
	}

	// I HATE THIS BUGGY WEB BROWSER ......

	switch path {
	case "06/INFOR/INFOR00.HTM": //インフォメーション
		tplUnderConstruction.Execute(tw, nil)
	case "06/V_RANK/TOTAL00.HTM": //通信対戦ランキング > 総合
		users, err := db.DefaultDB.GetWinCountRanking(0, 20, 0)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{Title: "勝利数ランキング(総合)"}
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
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/V_RANK/AEUG00.HTM": //通信対戦ランキング > 連邦エゥーゴ
		users, err := db.DefaultDB.GetWinCountRanking(0, 20, 1)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{Title: "勝利数ランキング(連邦・エゥーゴ)"}
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
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/V_RANK/TITANS00.HTM": //通信対戦ランキング > ジオンティターンズ
		users, err := db.DefaultDB.GetWinCountRanking(0, 20, 2)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{Title: "勝利数ランキング(ジオン・ティターンズ)"}
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
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/P_RANK/TOTAL00.HTM": //撃墜数ランキング > 総合
		users, err := db.DefaultDB.GetKillCountRanking(0, 20, 0)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{Title: "撃墜数ランキング(総合)"}
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
	case "06/P_RANK/AEUG00.HTM": //撃墜数ランキング > 連邦エゥーゴ
		users, err := db.DefaultDB.GetKillCountRanking(0, 20, 1)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{Title: "撃墜数ランキング(連邦・エゥーゴ)"}
		for _, u := range users {
			rp.Records = append(rp.Records, rankingParamRecord{
				Rank:   u.Rank,
				UserID: u.UserID,
				Name:   u.Name,
				Team:   u.Team,
				Score:  fmt.Sprintf("撃墜数：%d 機", u.AeugKillCount),
			})
		}
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/P_RANK/TITANS00.HTM": //撃墜数ランキング > ジオンティターンズ
		users, err := db.DefaultDB.GetKillCountRanking(0, 20, 2)
		if err != nil {
			glog.Errorln(err)
		}
		rp := rankingParams{Title: "撃墜数ランキング(ジオン・ティターンズ)"}
		for _, u := range users {
			rp.Records = append(rp.Records, rankingParamRecord{
				Rank:   u.Rank,
				UserID: u.UserID,
				Name:   u.Name,
				Team:   u.Team,
				Score:  fmt.Sprintf("撃墜数：%d 機", u.TitansKillCount),
			})
		}
		err = tplRanking.Execute(tw, rp)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/E_RANK/TOTAL00.HTM": //イベントランキング > 総合
		err := tplUnderConstruction.Execute(tw, nil)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/E_RANK/AEUG00.HTM": //イベントランキング > 連邦エゥーゴ
		err := tplUnderConstruction.Execute(tw, nil)
		if err != nil {
			glog.Errorln(err)
		}
	case "06/E_RANK/TITANS00.HTM": //イベントランキング > ジオンティターンズ
		err := tplUnderConstruction.Execute(tw, nil)
		if err != nil {
			glog.Errorln(err)
		}
	default:
		glog.Errorln("unknown page request", path)
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

var tplFuncs = template.FuncMap{
	"sub2": func(a, b, c int) int { return a - b - c },
}

var tplUnderConstruction = template.Must(template.New("unc").Funcs(tplFuncs).Parse(`
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
<FONT SIZE=5>
<iframe src="https://www.w3schools.com"></iframe>
</FONT>
</CENTER>

</BODY>
</HTML>
`))

var tplRanking = template.Must(template.New("ranking").Funcs(tplFuncs).Parse(`
<HTML>
<HEAD>
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
	<TITLE>{{.Title}}</TITLE>
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
</CENTER>

</BODY>
</HTML>
`))
