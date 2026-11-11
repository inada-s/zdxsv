package main

import (
	"flag"
	"fmt"

	"github.com/miekg/dns"
	"go.uber.org/zap"
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
			logger.Info("dns record", zap.Error(err))
		}
		m.Answer = append(m.Answer, rr)
		err = w.WriteMsg(m)
		if err != nil {
			logger.Info("dns write", zap.Error(err))
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
	loginIP = pick(*loginsvip, Conf.Login.PublicAddr)
	dnasIP = pick(*dnasip, Conf.DNAS.PublicAddr)
	gdxsvIP = pick(*gdxsvip, Conf.Login.PublicAddr)
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

// newDNSServer is the game's DNS server on config.Conf.DNS.Addr.
func newDNSServer() (*dns.Server, error) {
	loginPublicIP, dnassvIP, gdxsvPublicIP, err := dnsServerIPs()
	if err != nil {
		return nil, err
	}

	logger.Info("DNAS server", zap.Any("ip", dnassvIP))
	logger.Info("login server", zap.Any("ip", loginPublicIP))
	logger.Info("gdx server", zap.Any("ip", gdxsvPublicIP))

	return &dns.Server{Addr: Conf.DNS.Addr, Net: "udp", Handler: newDNSMux(loginPublicIP, dnassvIP, gdxsvPublicIP)}, nil
}

func mainDNS() {
	server, err := newDNSServer()
	if err != nil {
		logger.Fatal("dns", zap.Error(err))
	}
	logger.Info("DNS listen", zap.String("addr", Conf.DNS.Addr))
	logger.Fatal("dns server stopped", zap.Error(server.ListenAndServe()))
}
