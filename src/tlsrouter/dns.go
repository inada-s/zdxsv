package main

import (
	"flag"
	"log"
	"net"
	"os"
	"sync"

	"github.com/miekg/dns"
)

var (
	dnasip    = flag.String("dnasip", "", "Public IP Addr of DNAS server")
	loginsvip = flag.String("loginsvip", "", "Public Addr of Login server")

	lastResolved = LastResolved{m: map[string]string{}}
)

// LastResolved holds last resolved host by remote addr.
type LastResolved struct {
	mtx sync.Mutex
	m   map[string]string
}

// Update updates last resolved host.
func (x *LastResolved) Update(ip string, resolvedAddr string) {
	log.Println("LastResolved", ip, resolvedAddr)
	x.mtx.Lock()
	x.m[ip] = resolvedAddr
	x.mtx.Unlock()
}

// Get returns last resolved host.
func (x *LastResolved) Get(ip string) string {
	x.mtx.Lock()
	defer x.mtx.Unlock()
	return x.m[ip]
}

func dnsMain() {
	// dnassvIP := "149.56.101.45"
	dnassvIP := os.Getenv("ZDXSV_DNAS_IP")
	if *dnasip != "" {
		dnassvIP = *dnasip
	}

	loginPublicIP := os.Getenv("ZDXSV_LOGIN_PUBLIC_IP")
	if *loginsvip != "" {
		loginPublicIP = *loginsvip
	}

	log.Println("DNAS server ", dnassvIP)
	log.Println("Login server ", loginPublicIP)
	dns.HandleFunc("kddi-mmbb.jp", makeDNSHandler("kddi-mmbb.jp", "www01.kddi-mmbb.jp. 3600 IN A "+loginPublicIP))
	dns.HandleFunc("playstation.org", makeDNSHandler("playstation.org", "gate1.jp.dnas.playstation.org. 3600 IN A "+dnassvIP))

	server := &dns.Server{Addr: ":53", Net: "udp"}
	err := server.ListenAndServe()
	log.Fatal(err)
}

func addr2IPString(addr net.Addr) string {
	// Update last resolved record
	switch v := addr.(type) {
	case *net.UDPAddr:
		return v.IP.String()
	case *net.TCPAddr:
		return v.IP.String()
	case *net.IPAddr:
		return v.IP.String()
	default:
		log.Println("unknown remote addr type:", v)
		return ""
	}
}

func makeDNSHandler(hostname, record string) func(dns.ResponseWriter, *dns.Msg) {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		// Reply dns msg
		m := new(dns.Msg)
		m.SetReply(r)
		rr, err := dns.NewRR(record)
		if err != nil {
			log.Println(err)
		}
		m.Answer = append(m.Answer, rr)
		err = w.WriteMsg(m)
		if err != nil {
			log.Println(err)
		}

		// Update last resolved hostname
		lastResolved.Update(addr2IPString(w.RemoteAddr()), hostname)
	}
}
