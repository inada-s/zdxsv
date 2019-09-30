package main

import (
	"github.com/miekg/dns"
	"log"
)

func makeHandler(record string) func(dns.ResponseWriter, *dns.Msg) {
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

func mainDNS() {
	// dnassvIP := "149.56.101.45"
	dnassvIP := "194.135.89.81"
	log.Println("Login server ", conf.Login.PublicAddr)
	log.Println("DNAS server ", dnassvIP)
	dns.HandleFunc("kddi-mmbb.jp", makeHandler("www01.kddi-mmbb.jp. 3600 IN A "+conf.Login.PublicAddr))
	dns.HandleFunc("playstation.org", makeHandler("gate1.jp.dnas.playstation.org. 3600 IN A " + dnassvIP))
	server := &dns.Server{Addr: ":53", Net: "udp"}
	err := server.ListenAndServe()
	log.Println(err)
}
