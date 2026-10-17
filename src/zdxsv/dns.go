package main

import (
	"flag"
	"log"

	"zdxsv/pkg/config"

	"github.com/miekg/dns"
)

var (
	dnasip    = flag.String("dnasip", "", "Public IP Addr of DNAS server")
	loginsvip = flag.String("loginsvip", "", "Public Addr of Login server")
	gdxsvip   = flag.String("gdxsvip", "", "Public IP Addr of GundamDX server")
)

func makeDNSHandler(record string) func(dns.ResponseWriter, *dns.Msg) {
	return func(w dns.ResponseWriter, r *dns.Msg) {
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
	}
}

// newDNSMux answers the names the game resolves with the public server addresses.
func newDNSMux(loginIP, dnasIP, gdxsvIP string) *dns.ServeMux {
	mux := dns.NewServeMux()
	mux.HandleFunc("kddi-mmbb.jp", makeDNSHandler("www01.kddi-mmbb.jp. 3600 IN A "+loginIP))
	mux.HandleFunc("playstation.org", makeDNSHandler("gate1.jp.dnas.playstation.org. 3600 IN A "+dnasIP))
	mux.HandleFunc("ca1203.mmcp6", makeDNSHandler("ca1203.mmcp6. 3600 IN A "+gdxsvIP))
	mux.HandleFunc("ca1202.mmcp6", makeDNSHandler("ca1202.mmcp6. 3600 IN A "+gdxsvIP))
	return mux
}

func mainDNS() {
	dnassvIP := *dnasip
	if *dnasip == "" {
		dnassvIP = config.Conf.DNAS.PublicAddr
	}

	loginPublicIP := *loginsvip
	if *loginsvip == "" {
		loginPublicIP = config.Conf.Login.PublicAddr
	}

	gdxsvPublicIP := *gdxsvip
	if *gdxsvip == "" {
		gdxsvPublicIP = config.Conf.Login.PublicAddr
	}

	log.Println("DNAS server ", dnassvIP)
	log.Println("Login server ", loginPublicIP)
	log.Println("gdx server", gdxsvPublicIP)

	server := &dns.Server{Addr: ":53", Net: "udp", Handler: newDNSMux(loginPublicIP, dnassvIP, gdxsvPublicIP)}
	err := server.ListenAndServe()
	log.Fatal(err)
}
