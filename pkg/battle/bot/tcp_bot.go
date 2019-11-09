package bot

import (
	"net"
	"time"

	"github.com/golang/glog"
)

type TCPBot struct {
	botBase
	conn *net.TCPConn
}

func NewTCPBot(id int, sessionId int, players int, addr string) Bot {
	return &TCPBot{
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

func (bot *TCPBot) Run(fin <-chan interface{}) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", bot.addr)
	if err != nil {
		return err
	}
	bot.conn, err = net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return err
	}
	defer func() {
		bot.conn.Close()
	}()

	recvbuf := make([]byte, 1024)
	incoming := []byte{}

	bot.conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := bot.conn.Read(recvbuf)
	if err != nil {
		return err
	}

	if 12 < n {
		glog.Fatalln("Too long first data", recvbuf[:n])
	} else {
		firstData := []byte{130, 2, 16, 49, 0, 10, 0, 1, 0, 255, 255, 255}
		firstData = append(firstData, []byte(EncodeSessionId(bot.sessionId))...)
		_, err = bot.conn.Write(firstData)
		if err != nil {
			return err
		}
	}

	time.Sleep(time.Second)

	for {
		select {
		case <-fin:
			return nil
		default:
			sum := 0
			t := time.Now()
			sendbuf := newMsg(bot.id, bot.sendcnt, t)
			for sum < len(sendbuf) {
				bot.conn.SetWriteDeadline(t.Add(time.Second))
				n, err := bot.conn.Write(sendbuf[sum:])
				if err != nil {
					return err
				}
				sum += n
			}
			bot.sendcnt++

			msgcnt := 0
			for msgcnt < bot.players-1 {
				bot.conn.SetReadDeadline(time.Now().Add(time.Second))
				n, err := bot.conn.Read(recvbuf)
				if err != nil {
					return err
				}
				bot.readcnt++

				incoming = append(incoming, recvbuf[:n]...)

				k := int(incoming[0])
				for k <= len(incoming) {
					if glog.V(2) {
						glog.Infof("[TCP] id:%v SV>CL:%v", bot.id, incoming[:k])
					}
					msg := readMsg(incoming[:k])
					ms := (time.Now().UnixNano() - msg.unixnano)
					if bot.sendcnt > 100 && ms > bot.maxrtt {
						bot.maxrtt = ms
					}
					incoming = incoming[k:]

					bot.recvcnt++
					msgcnt++
					if len(incoming) == 0 {
						break
					} else {
						k = int(incoming[0])
					}
				}
			}
		}
	}
}

func (bot *TCPBot) Summary() {
	glog.Infof("[TCP] id:%v sendcnt:%v recvcnt:%v readcnt:%v maxrtt:%v[ns]\n", bot.id, bot.sendcnt, bot.recvcnt, bot.readcnt, bot.maxrtt)
}
