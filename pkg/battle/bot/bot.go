package bot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/rpc"
	"time"

	"zdxsv/pkg/battle/battlerpc"
)

type botBase struct {
	id        int
	sessionId int
	sendcnt   int32
	recvcnt   int32
	readcnt   int32
	players   int
	addr      string
	maxrtt    int64
}

type Bot interface {
	Run(<-chan interface{}) error
	Summary()
}

type message struct {
	size     byte
	id       int
	sendcnt  int32
	unixnano int64
}

func newMsg(id int, sendcnt int32, t time.Time) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, id)
	binary.Write(buf, binary.BigEndian, sendcnt)
	binary.Write(buf, binary.BigEndian, t.UnixNano())
	bytes := buf.Bytes()
	ret := make([]byte, 1, 1+len(bytes))
	ret[0] = byte(1 + len(bytes))
	ret = append(ret, bytes...)
	return ret
}

func readMsg(in []byte) message {
	msg := message{}
	buf := bytes.NewReader(in)
	binary.Read(buf, binary.BigEndian, &msg.size)
	binary.Read(buf, binary.BigEndian, &msg.id)
	binary.Read(buf, binary.BigEndian, &msg.sendcnt)
	binary.Read(buf, binary.BigEndian, &msg.unixnano)
	return msg
}

func EncodeSessionId(sessionId int) string {
		return fmt.Sprintf("%010d", sessionId+100001)
}

func ToStringSessionId(sessionId int) string {
		return fmt.Sprintf("%08d", sessionId)
}

func PrepareServer(n int, remote string) ([]int, error) {
	args := battlerpc.BattleInfoArgs{}
	ret := make([]int, n)
	for i := 0; i < n; i++ {
		args.Users = append(args.Users, battlerpc.User{
			UserId:    fmt.Sprintf("%06d", i),
			SessionId: fmt.Sprintf("%08d", i),
			Name:      fmt.Sprintf("%d", i),
			Team:      "",
			Entry:     byte(i%2 + 1),
			P2PMap:    map[string]struct{}{},
		})
		ret[i] = i
	}
	client, err := rpc.DialHTTP("tcp", remote)
	if err != nil {
		return nil, err
	}
	var reply int
	return ret, client.Call("Logic.NotifyBattleUsers", args, &reply)
}
