package bot

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"

	"zdxsv/pkg/proto"
)

type UDPBot struct {
	botBase
	waitmsg int32
	conn    *net.UDPConn
}

func NewUDPBot(id int, sessionId int, players int, addr string) Bot {
	return &UDPBot{
		botBase: botBase{
			id:        id,
			sessionId: sessionId,
			sendcnt:   0,
			recvcnt:   0,
			players:   players,
			addr:      addr,
			maxrtt:    0,
		},
	}
}

func (bot *UDPBot) Run(fin <-chan interface{}) error {
	udpAddr, err := net.ResolveUDPAddr("udp", bot.addr)
	if err != nil {
		return err
	}
	bot.conn, err = net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return err
	}
	defer bot.conn.Close()

	send := make(chan bool, 32)
	bb := proto.NewBattleBuffer(fmt.Sprintf("%6d", bot.id))
	var otherIds []string
	for i := 0; i < bot.players; i++ {
		if i != bot.id {
			otherIds = append(otherIds, fmt.Sprintf("%6d", i))
		}
	}
	mf := proto.NewMessageFilter(otherIds)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := proto.NewUDPClient(bot.conn)
	go client.ReadLoop(ctx)

	var start atomic.Value
	start.Store(false)

	defer client.Unsubscribe(fmt.Sprint(bot.id))
	client.SubscribePacket(fmt.Sprint(bot.id), func(pkt *proto.Packet, addr *net.UDPAddr) {
		switch pkt.GetType() {
		case proto.MessageType_HelloServer:
			if pkt.GetHelloServerData().GetOk() {
				start.Store(true)
			} else {
				glog.Errorln("HelloServer failed")
			}
		case proto.MessageType_Battle:
			if glog.V(3) {
				glog.Infof("[UDP] id:%v SV>CL:%v", bot.id, pkt)
			}
			bb.ApplySeqAck(pkt.GetSeq(), pkt.GetAck())
			for _, msg := range pkt.GetBattleData() {
				if mf.Filter(msg) {
					bot.recvcnt++
					frame := msg.GetBody()
					for 0 < len(frame) {
						k := int(frame[0])
						if glog.V(2) {
							glog.Infof("[UDP] id:%v SV>CL:%v", bot.id, frame)
						}
						msg := readMsg(frame[:k])
						ms := (time.Now().UnixNano() - msg.unixnano)

						if atomic.LoadInt32(&bot.sendcnt) > 100 && ms > bot.maxrtt {
							bot.maxrtt = ms
						}
						frame = frame[k:]

						atomic.AddInt32(&bot.readcnt, 1)
						if atomic.AddInt32(&bot.waitmsg, -1) == 0 {
							send <- true
						}
					}
				}
			}
		}
	})

	pkt := new(proto.Packet)
	pkt.Type = proto.MessageType_HelloServer.Enum()
	pkt.HelloServerData = &proto.HelloServerMessage{
		SessionId: pb.String(ToStringSessionId(bot.sessionId)),
	}

	for i := 0; i < 10; i++ {
		if !start.Load().(bool) {
			client.SendPacket(pkt)
			time.Sleep(100 * time.Millisecond)
		}
	}

	if !start.Load().(bool) {
		return fmt.Errorf("HelloServer failed")
	}

	firstData := []byte{130, 2, 16, 49, 0, 10, 0, 1, 0, 255, 255, 255}
	firstData = append(firstData, []byte(EncodeSessionId(bot.sessionId))...)
	msg := mf.GenerateMessage(fmt.Sprintf("%06d", bot.id), firstData)
	bb.PushBattleMessage(msg)
	data, seq, ack := bb.GetSendData()
	pkt.Reset()
	pkt.Type = proto.MessageType_Battle.Enum()
	pkt.BattleData = data
	pkt.Seq = pb.Uint32(seq)
	pkt.Ack = pb.Uint32(ack)
	client.SendPacket(pkt)

	send <- true
	atomic.StoreInt32(&bot.waitmsg, int32(bot.players-1))

	for {
		select {
		case <-fin:
			return nil
		case <-send:
			msg := mf.GenerateMessage(fmt.Sprintf("%06d", bot.id), newMsg(bot.id, bot.sendcnt, time.Now()))
			bb.PushBattleMessage(msg)
			data, seq, ack := bb.GetSendData()
			pkt.Reset()
			pkt.Type = proto.MessageType_Battle.Enum()
			pkt.BattleData = data
			pkt.Seq = pb.Uint32(seq)
			pkt.Ack = pb.Uint32(ack)
			client.SendPacket(pkt)
			atomic.AddInt32(&bot.sendcnt, 1)
			atomic.StoreInt32(&bot.waitmsg, int32(bot.players-1))
		}
	}
	return nil
}

func (bot *UDPBot) Summary() {
	glog.Infof("[UDP] id:%v sendcnt:%v recvcnt:%v readcnt:%v maxrtt:%v[ns]\n", bot.id, bot.sendcnt, bot.recvcnt, bot.readcnt, bot.maxrtt)
}
