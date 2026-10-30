package udptest

import (
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

const helloPayload = "Hello"

var allowPrivate = os.Getenv("UDPTEST_ALLOW_PRIVATE") == "1"

func init() {
	functions.HTTP("FunctionEntryPoint", Handler)
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseMultipartForm(64 * 1024); err != nil && err != http.ErrNotMultipart {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	addr, err := parseAddr(r.FormValue("addr"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		log.Printf("dial %v: %v", addr, err)
		http.Error(w, "send failed", http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(helloPayload)); err != nil {
		log.Printf("send %v: %v", addr, err)
		http.Error(w, "send failed", http.StatusInternalServerError)
		return
	}
	w.Write([]byte("Sent"))
}

type addrError string

func (e addrError) Error() string { return string(e) }

func parseAddr(s string) (netip.AddrPort, error) {
	if s == "" {
		return netip.AddrPort{}, addrError("No addr")
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return netip.AddrPort{}, addrError("addr must be ip:port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, addrError("addr must be an IP literal")
	}
	ip = ip.Unmap()
	if !allowPrivate && (!ip.IsGlobalUnicast() || ip.IsPrivate()) {
		return netip.AddrPort{}, addrError("addr must be a public IP")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return netip.AddrPort{}, addrError("bad port")
	}
	return netip.AddrPortFrom(ip, uint16(port)), nil
}
