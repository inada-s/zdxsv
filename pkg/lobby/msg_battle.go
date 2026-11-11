package lobby

import (
	"strconv"
	"strings"
	. "zdxsv/pkg/message"

	"zdxsv/pkg/lobby/model"
	"zdxsv/pkg/proto"

	"encoding/json"

	pb "github.com/golang/protobuf/proto"
	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

func NoticeBattleStart(p *LbsPeer) {
	n := NewServerNotice(0x6910)
	p.SendMessage(n)
}

var _ = register(0x6911, "GetBattleUserCount", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	count := p.app.OnGetBattleUserCount(p)
	w := a.Writer()
	if count > 0 {
		w.Write8(count)
	} else {
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6912, "GetBattleUserPosition", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	w := a.Writer()
	pos := p.app.OnGetBattleUserPosition(p)
	if pos > 0 {
		w.Write8(pos)
	} else {
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6913, "GetBattleOpponentUser", func(p *LbsPeer, m *LbsMessage) {
	pos := m.Reader().Read8()
	user := p.app.OnGetBattleOpponentUser(p, pos)
	a := NewServerAnswer(m)
	w := a.Writer()
	w.Write8(pos)
	if user != nil {
		w.Write8(user.Entry)
		w.WriteString(user.UserID)
		w.WriteString(user.Name)
		w.WriteString(user.Team)
		w.WriteString(user.Bin)
		w.Write8(pos) // 不明
	} else {
		zlog.Info("UserPos not found", zap.Any("pos", pos))
		a.Status = StatusError
	}
	p.SendMessage(a)
})

var _ = register(0x6917, "GetBattleOpponentStatus", func(p *LbsPeer, m *LbsMessage) {
	pos := m.Reader().Read8()
	a := NewServerAnswer(m)
	w := a.Writer()
	user := p.app.OnGetBattleOpponentUser(p, pos)
	w.Write8(pos)
	if user == nil {
		zlog.Info("UserPos not found", zap.Any("pos", pos))
		a.Status = StatusError
		p.SendMessage(a)
		return
	}

	w.Write16(p.app.OnGetBattleOpponentClass(user))
	w.Write32(0) // Unknown
	w.Write32(uint32(user.BattleCount))
	w.Write32(uint32(user.WinCount))
	w.Write32(uint32(user.LoseCount))
	w.Write32(0) // Unknown

	p.SendMessage(a)
})

var _ = register(0x6914, "GetBattleRule", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	w := a.Writer()
	rule := p.app.OnGetBattleRule(p)
	if rule == nil {
		a.Status = StatusError
	} else {
		w.Write(rule.Serialize())
	}
	p.SendMessage(a)
})

var _ = register(0x6915, "GetBattleBattleCode", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	battleCode, err := p.app.OnGetBattleCode(p)
	if err == nil {
		w := a.Writer()
		w.WriteString(battleCode)
	} else {
		zlog.Info("failed to get battle code")
		a.Status = StatusError
	}
	p.SendMessage(a)
})

// P2PMatchingReport is sent by emulators with lobby GGPO on the first lobby connection
// after a battle, after the platform info: a P2PMatchingReport (zdxsv.proto) on how the
// battle ran. Custom category, no answer: logged for server-side stats and troubleshooting.
var _ = register(0x9952, "P2PMatchingReport", func(p *LbsPeer, m *LbsMessage) {
	report := &proto.P2PMatchingReport{}
	if err := pb.Unmarshal(m.Body, report); err != nil {
		zlog.Warn("p2p matching report: bad request", zap.String("user_id", p.UserID), zap.Error(err))
		return
	}
	p.app.OnP2PMatchingReport(p, report)
})

func battleInfoNotice(info *proto.P2PMatching) *LbsMessage {
	if info == nil {
		return nil
	}
	body, err := pb.Marshal(info)
	if err != nil {
		zlog.Error("battle info failed", zap.Error(err))
		return nil
	}
	n := NewServerNotice(0x9951)
	n.Category = CategoryCustom
	n.Body = body
	return n
}

var _ = register(0x6916, "GetBattleServerAddress", func(p *LbsPeer, m *LbsMessage) {
	ip, port := p.app.OnGetBattleServerAddress(p)
	if n := battleInfoNotice(p.app.OnGetBattleInfo(p)); n != nil {
		zlog.Info("udp bridge battle info", zap.String("user_id", p.UserID))
		p.SendMessage(n)
	}
	a := NewServerAnswer(m)

	if ip == nil || ip.To4() == nil || port == 0 {
		a.Status = StatusError
	} else {
		bits := strings.Split(ip.String(), ".")
		b0, _ := strconv.Atoi(bits[0])
		b1, _ := strconv.Atoi(bits[1])
		b2, _ := strconv.Atoi(bits[2])
		b3, _ := strconv.Atoi(bits[3])
		w := a.Writer()
		w.Write16(4)
		w.Write8(byte(b0))
		w.Write8(byte(b1))
		w.Write8(byte(b2))
		w.Write8(byte(b3))
		w.Write16(2)
		w.Write16(port)
	}
	p.SendMessage(a)
})

var _ = register(0x6210, "EnterBattleAfterRoom", func(p *LbsPeer, m *LbsMessage) {
	p.app.OnEnterBattleAfterRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6211, "ExitBattleAfterRoom", func(p *LbsPeer, m *LbsMessage) {
	p.app.OnExitBattleAfterRoom(p)
	p.SendMessage(NewServerAnswer(m))
})

var _ = register(0x6212, "GetBattleAfterRoomUserCount", func(p *LbsPeer, m *LbsMessage) {
	a := NewServerAnswer(m)
	w := a.Writer()
	n := p.app.OnGetBattleAfterRoomUserCount(p)
	w.Write16(n)
	p.SendMessage(a)
})

func NoticeBattleAfterRoomUserCount(p *LbsPeer, count uint16) {
	n := NewServerNotice(0x6212)
	w := n.Writer()
	w.Write16(count)
	p.SendMessage(n)
}

func AskBattleResult(p *LbsPeer) {
	p.SendMessage(NewServerQuestion(0x6138))
}

func parseBattleResult(m *LbsMessage) *model.BattleResult {
	r := m.Reader()
	v01 := r.Read16()
	v02 := r.ReadEncryptedString()
	v03 := r.Read8()
	v04 := r.Read8()
	v05 := r.Read8()
	v06 := r.Read8()
	v07 := r.Read8()
	v08 := r.Read8()
	v09 := r.Read8()
	v10 := r.Read32()
	v11 := r.Read32()
	v12 := r.Read32()
	v13 := r.Read32()
	v14 := r.Read32()
	v15 := r.Read8()
	v16 := r.Read8()
	v17 := r.Read8()
	v18 := r.Read8()
	v19 := r.Read8()
	v20 := r.Read16()
	v21 := r.Read16()
	v22 := r.Read16()
	v23 := r.Read16()
	v24 := r.Read16()
	v25 := r.Read16()
	v26 := r.Read16()
	v27 := r.Read16()
	return &model.BattleResult{
		Unk01: v01, BattleCode: v02, Unk03: v03, Unk04: v04,
		Unk05: v05, Unk06: v06, BattleCount: v07, WinCount: v08,
		LoseCount: v09, KillCount: v10, DeathCount: v11, TotalFrame: v12,
		Unk13: v13, Unk14: v14, Side: v15, Unk16: v16,
		Unk17: v17, Unk18: v18, Unk19: v19, Unk20: v20,
		Unk21: v21, Unk22: v22, Unk23: v23, Unk24: v24,
		Unk25: v25, Unk26: v26, Unk27: v27,
	}
}

var _ = register(0x6138, "AnswerBattleResult", func(p *LbsPeer, m *LbsMessage) {
	result := parseBattleResult(m)
	js, _ := json.MarshalIndent(result, "", "  ")
	zlog.Info("battle result", zap.String("user_id", p.LbsUser.UserID), zap.String("name", p.LbsUser.Name), zap.String("result", string(js)))

	p.app.OnGetBattleResult(p, result)
})
