package main

import (
	"net"
	"strings"
	"testing"

	"zdxsv/pkg/config"

	"github.com/miekg/dns"
)

func TestDNSAnswers(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: newDNSMux("10.0.0.1", "10.0.0.2", "10.0.0.3"), NotifyStartedFunc: func() { close(started) }}
	go srv.ActivateAndServe()
	<-started
	t.Cleanup(func() { srv.Shutdown() })

	query := func(name string) *dns.Msg {
		t.Helper()
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		r, _, err := new(dns.Client).Exchange(q, pc.LocalAddr().String())
		if err != nil {
			t.Fatal(name, err)
		}
		return r
	}
	for _, c := range []struct{ name, answer, ip string }{
		{"www01.kddi-mmbb.jp.", "www01.kddi-mmbb.jp.", "10.0.0.1"},
		{"gate1.jp.dnas.playstation.org.", "gate1.jp.dnas.playstation.org.", "10.0.0.2"},
		{"ca1203.mmcp6.", "ca1203.mmcp6.", "10.0.0.3"},
		{"ca1202.mmcp6.", "ca1202.mmcp6.", "10.0.0.3"},
		// A zone answers each name in it with its one record.
		{"other.kddi-mmbb.jp.", "www01.kddi-mmbb.jp.", "10.0.0.1"},
	} {
		r := query(c.name)
		if len(r.Question) != 1 || r.Question[0].Name != c.name {
			t.Errorf("%s: reply question %v", c.name, r.Question)
		}
		if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
			t.Errorf("%s: rcode %d answers %v", c.name, r.Rcode, r.Answer)
			continue
		}
		a, ok := r.Answer[0].(*dns.A)
		if !ok || a.Hdr.Name != c.answer || a.A.String() != c.ip || a.Hdr.Ttl != 3600 {
			t.Errorf("%s: answer %v, want %s A %s", c.name, r.Answer[0], c.answer, c.ip)
		}
	}
	if r := query("example.com."); r.Rcode != dns.RcodeRefused || len(r.Answer) != 0 {
		t.Errorf("example.com.: rcode %d answers %v", r.Rcode, r.Answer)
	}
}

func TestDNSServerIPs(t *testing.T) {
	saved := config.Conf
	t.Cleanup(func() { config.Conf = saved; *loginsvip, *dnasip, *gdxsvip = "", "", "" })

	config.Conf.Login.PublicAddr = "10.0.0.1"
	config.Conf.DNAS.PublicAddr = "10.0.0.2"
	if l, d, g, err := dnsServerIPs(); err != nil || l != "10.0.0.1" || d != "10.0.0.2" || g != "10.0.0.1" {
		t.Errorf("from config: %q %q %q %v", l, d, g, err)
	}
	*gdxsvip = "10.0.0.3"
	if l, d, g, err := dnsServerIPs(); err != nil || l != "10.0.0.1" || d != "10.0.0.2" || g != "10.0.0.3" {
		t.Errorf("-gdxsvip: %q %q %q %v", l, d, g, err)
	}

	config.Conf.DNAS.PublicAddr = ""
	if _, _, _, err := dnsServerIPs(); err == nil || !strings.Contains(err.Error(), "DNAS") {
		t.Errorf("no DNAS IP: %v", err)
	}
	*dnasip = "10.0.0.2"
	config.Conf.Login.PublicAddr = ""
	if _, _, _, err := dnsServerIPs(); err == nil || !strings.Contains(err.Error(), "login") {
		t.Errorf("no login IP: %v", err)
	}
	*loginsvip = "10.0.0.1"
	*gdxsvip = ""
	if _, _, _, err := dnsServerIPs(); err == nil || !strings.Contains(err.Error(), "gdx") {
		t.Errorf("no gdx IP: %v", err)
	}
}
