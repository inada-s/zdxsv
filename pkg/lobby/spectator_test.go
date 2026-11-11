package lobby

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func liveServer(t *testing.T) (*SpectatorRegistry, *httptest.Server) {
	r := NewSpectatorRegistry()
	stop := make(chan struct{})
	go r.Run(stop)
	sv := httptest.NewServer(&APIHandler{Live: r})
	t.Cleanup(func() {
		close(stop)
		sv.Close()
	})
	return r, sv
}

type liveUpload struct {
	w    *io.PipeWriter
	done chan int
}

func startUpload(t *testing.T, url, code, session string) *liveUpload {
	pr, pw := io.Pipe()
	u := &liveUpload{w: pw, done: make(chan int, 1)}
	go func() {
		res, err := http.Post(url+"/lbs/live_stream?battle_code="+code+"&session_id="+session, "application/octet-stream", pr)
		if err != nil {
			u.done <- -1
			return
		}
		res.Body.Close()
		u.done <- res.StatusCode
	}()
	return u
}

func readUntil(t *testing.T, r io.Reader, want []byte, within time.Duration) []byte {
	t.Helper()
	got := make(chan []byte, 1)
	go func() {
		var b []byte
		buf := make([]byte, 4096)
		for !bytes.HasPrefix(b, want) || len(b) < len(want) {
			n, err := r.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				break
			}
		}
		got <- b
	}()
	select {
	case b := <-got:
		return b
	case <-time.After(within):
		t.Fatalf("no %q within %v", want, within)
		return nil
	}
}

func TestLiveStream(t *testing.T) {
	r, sv := liveServer(t)
	if res, _ := http.Get(sv.URL + "/lbs/live_stream?battle_code=B1"); res.StatusCode != 404 {
		t.Fatalf("unknown battle: %d", res.StatusCode)
	}
	r.Open("B1", 7, nil, time.Now())
	if u := startUpload(t, sv.URL, "B1", "8"); func() int { u.w.Close(); return <-u.done }() != 404 {
		t.Fatal("wrong session accepted")
	}
	up := startUpload(t, sv.URL, "B1", "7")
	up.w.Write([]byte("head"))
	waitFor(t, "the first upload", func() bool { l := r.List(); return len(l) == 1 && l[0].Bytes == 4 })
	if u := startUpload(t, sv.URL, "B1", "7"); func() int { u.w.Close(); return <-u.done }() != 409 {
		t.Fatal("second upload accepted")
	}

	// A spectator that never reads: its socket fills up.
	stalled, err := net.Dial("tcp", strings.TrimPrefix(sv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	stalled.(*net.TCPConn).SetReadBuffer(4096)
	stalled.Write([]byte("GET /lbs/live_stream?battle_code=B1 HTTP/1.1\r\nHost: x\r\n\r\n"))

	a, err := http.Get(sv.URL + "/lbs/live_stream?battle_code=B1")
	if err != nil || a.StatusCode != 200 || a.Header.Get("X-Battle-Code") != "B1" {
		t.Fatalf("watch: %v %v", err, a)
	}
	defer a.Body.Close()
	readUntil(t, a.Body, []byte("head"), 2*time.Second)

	big := bytes.Repeat([]byte("x"), 8<<20)
	up.w.Write(big)
	start := time.Now()
	readUntil(t, a.Body, big, 10*time.Second)
	t.Log("8 MB to a reader beside a stalled one:", time.Since(start))

	// A late spectator gets the stream from its start; the list counts all three.
	b, err := http.Get(sv.URL + "/lbs/live_stream")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Body.Close()
	if b.Header.Get("X-Battle-Code") != "B1" {
		t.Fatal("newest: ", b.Header)
	}
	readUntil(t, b.Body, append([]byte("head"), big...), 10*time.Second)
	if l := r.List(); len(l) != 1 || l[0].Spectators != 3 || l[0].Closed || l[0].Bytes != 4+len(big) {
		t.Fatalf("list %+v", l[0])
	}

	up.w.Write([]byte("end"))
	up.w.Close()
	if code := <-up.done; code != 204 {
		t.Fatalf("upload: %d", code)
	}
	for _, body := range []io.Reader{a.Body, b.Body} {
		rest, err := io.ReadAll(body)
		if err != nil || !bytes.HasSuffix(rest, []byte("end")) {
			t.Fatalf("after the end: %v %q", err, rest[max(0, len(rest)-8):])
		}
	}
	if l := r.List(); !l[0].Closed {
		t.Fatal("not closed")
	}
	if res, _ := http.Get(sv.URL + "/lbs/live_stream?skip="); res.StatusCode != 404 {
		t.Fatal("auto-next picked an ended battle")
	}
}

func TestLiveUploadIdle(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	r, sv := liveServer(t)
	r.Open("B1", 7, nil, time.Now())
	up := startUpload(t, sv.URL, "B1", "7")
	up.w.Write([]byte("h"))
	select {
	case <-up.done:
	case <-time.After(liveUploadIdle + 5*time.Second):
		t.Fatal("silent upload kept open")
	}
	if l := r.List(); !l[0].Closed || l[0].Bytes != 1 {
		t.Fatalf("list %+v", l[0])
	}
}
