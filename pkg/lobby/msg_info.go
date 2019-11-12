package lobby

import (
	"html/template"
	"zdxsv/pkg/db"
	. "zdxsv/pkg/lobby/message"

	"github.com/golang/glog"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

var _ = register(0x6801, "GetRegurationData", func(p *AppPeer, m *Message) {
	path := m.Reader().ReadEncryptedString()
	a := NewServerAnswer(m)
	tw := transform.NewWriter(a.Writer(), japanese.EUCJP.NewEncoder())
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

	type rankingParams struct {
		Title   string
		Records []*db.RankingRecord
	}

	// I HATE THIS BUGGY WEB BROWSER ......

	switch path {
	case "06/INFOR/INFOR00.HTM": //インフォメーション
		tplUnderConstruction.Execute(tw, nil)
	case "06/V_RANK/TOTAL00.HTM": //通信対戦ランキング > 総合
		users, err := db.DefaultDB.GetWinCountRanking(0, 10, 0)
		if err != nil {
			glog.Errorln(err)
		}
		err = tplRanking.Execute(tw, rankingParams{
			Title:   "勝利数ランキング(総合)",
			Records: users[:3],
		})
		if err != nil {
			glog.Errorln(err)
		}
	case "06/V_RANK/AEUG00.HTM": //通信対戦ランキング > 連邦エゥーゴ
		users, err := db.DefaultDB.GetWinCountRanking(0, 10, 1)
		if err != nil {
			glog.Errorln(err)
		}
		err = tplRanking.Execute(tw, rankingParams{
			Title:   "勝利数ランキング(連邦・エゥーゴ)",
			Records: users,
		})
		if err != nil {
			glog.Errorln(err)
		}
	case "06/V_RANK/TITANS00.HTM": //通信対戦ランキング > ジオンティターンズ
		users, err := db.DefaultDB.GetWinCountRanking(0, 10, 2)
		if err != nil {
			glog.Errorln(err)
		}
		err = tplRanking.Execute(tw, rankingParams{
			Title:   "勝利数ランキング(ジオン・エゥーゴ)",
			Records: users,
		})
		if err != nil {
			glog.Errorln(err)
		}
	case "06/P_RANK/TOTAL00.HTM": //撃墜数ランキング > 総合
		users, err := db.DefaultDB.GetKillCountRanking(0, 10, 0)
		if err != nil {
			glog.Errorln(err)
		}
		err = tplRanking.Execute(tw, rankingParams{
			Title:   "撃墜数ランキング(総合)",
			Records: users,
		})
		if err != nil {
			glog.Errorln(err)
		}
	case "06/P_RANK/AEUG00.HTM": //撃墜数ランキング > 連邦エゥーゴ
		users, err := db.DefaultDB.GetKillCountRanking(0, 10, 1)
		if err != nil {
			glog.Errorln(err)
		}
		err = tplRanking.Execute(tw, rankingParams{
			Title:   "撃墜数ランキング(連邦・エゥーゴ)",
			Records: users,
		})
		if err != nil {
			glog.Errorln(err)
		}
	case "06/P_RANK/TITANS00.HTM": //撃墜数ランキング > ジオンティターンズ
		users, err := db.DefaultDB.GetKillCountRanking(0, 10, 2)
		if err != nil {
			glog.Errorln(err)
		}
		err = tplRanking.Execute(tw, rankingParams{
			Title:   "撃墜数ランキング(ジオン・ティターンズ)",
			Records: users,
		})
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
	p.SendMessage(a)
})

var tplUnderConstruction = template.Must(template.New("unc").Parse(`
<HTML>
<HEAD>
	<TITLE> UNDER CONSTRUCTION </TITLE>
</HEAD>
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
</FONT>
</CENTER>

</BODY>
</HTML>
`))

var tplRanking = template.Must(template.New("ranking").Parse(`
<HTML>
<HEAD> <TITLE> {{.Title}} </TITLE> </HEAD>

<!--
<GAME-STYLE>
	"MOUSE=OFF",
	"SCROLL=OFF",
	"TITLE=OFF",
	"BACK=ON:mmbb://BUTTON_NG",
	"FORWARD=OFF",
	"CANCEL=OFF",
	"RELOAD=ON",
	"X_SHOW=ON",
	"LINK_U=OFF",
</GAME-STYLE>
-->

<BODY BGCOLOR=#000000 background=afs://02/114.PNG text=white link=white vlink=white>
  <TABLE WIDTH=584 CELLSPACING=0 CELLPADDING=0>
    <!-- タイトル -->
    <TR>
      <TD BACKGROUND=afs://02/121.PNG WIDTH=256 HEIGHT=44>
      <TD BACKGROUND=afs://02/122.PNG WIDTH=32 HEIGHT=44>
      <TD BACKGROUND=afs://02/123.PNG WIDTH=296 HEIGHT=44>
    </TR>

    <TR> <TD COLSPAN=3>

    <!-- 項目 -->
    <CENTER>
      <FONT SIZE=3>
        <TABLE WIDTH=550 CELLSPACING=0 CELLPADDING=0>
          <TR> <TD COLSPAN=6 HEIGHT=10>

          {{range .Records}}
			<TR> <TD BGCOLOR=#ff7500 COLSPAN=6 HEIGHT=2>
			<TR>
				<TD BGCOLOR=#ff7500 WIDTH=5 HEIGHT=30>
				<TD BGCOLOR=#000000 WIDTH=80  >1位
				<TD BGCOLOR=#000000 WIDTH=60  >aaa
				<TD BGCOLOR=#000000 WIDTH=200 >bbb
				<TD BGCOLOR=#000000 WIDTH=200 >ccc
				<TD BGCOLOR=#ff7500 WIDTH=5>
			<TR> <TD BGCOLOR=#ff7500 COLSPAN=6 HEIGHT=2>
		  {{end}}

        </TABLE>
      </FONT>
    </CENTER>
  </TABLE>
</BODY>
</HTML>
`))
