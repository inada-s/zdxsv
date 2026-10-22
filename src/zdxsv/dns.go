package main

import (
	"flag"
	"fmt"
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

// dnsServerIPs returns the addresses the DNS server answers with: a flag if
// set, else the config. An empty one is an error: its answers would be broken.
func dnsServerIPs() (loginIP, dnasIP, gdxsvIP string, err error) {
	pick := func(flagValue, conf string) string {
		if flagValue != "" {
			return flagValue
		}
		return conf
	}
	loginIP = pick(*loginsvip, config.Conf.Login.PublicAddr)
	dnasIP = pick(*dnasip, config.Conf.DNAS.PublicAddr)
	gdxsvIP = pick(*gdxsvip, config.Conf.Login.PublicAddr)
	for _, c := range []struct{ name, ip string }{
		{"login (-loginsvip, ZDXSV_LOGIN_PUBLIC_ADDR)", loginIP},
		{"DNAS (-dnasip, ZDXSV_DNAS_PUBLIC_ADDR)", dnasIP},
		{"gdx (-gdxsvip, ZDXSV_LOGIN_PUBLIC_ADDR)", gdxsvIP},
	} {
		if c.ip == "" {
			return "", "", "", fmt.Errorf("no public IP for the %s server", c.name)
		}
	}
	return loginIP, dnasIP, gdxsvIP, nil
}

func mainDNS() {
	loginPublicIP, dnassvIP, gdxsvPublicIP, err := dnsServerIPs()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("DNAS server ", dnassvIP)
	log.Println("Login server ", loginPublicIP)
	log.Println("gdx server", gdxsvPublicIP)

	log.Println("DNS listen", config.Conf.DNS.Addr)
	server := &dns.Server{Addr: config.Conf.DNS.Addr, Net: "udp", Handler: newDNSMux(loginPublicIP, dnassvIP, gdxsvPublicIP)}
	err = server.ListenAndServe()
	log.Fatal(err)
}
