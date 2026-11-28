package main

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGCS answers the JSON API calls of GCSReplayStore.Put: no object exists; an upload is answered
// uploadCode once its body is read to the end, and counted in stored when that is 200.
type fakeGCS struct {
	uploadCode int
	mu         sync.Mutex
	uploads    int
	stored     int
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	switch {
	case !strings.HasPrefix(r.URL.Path, "/upload/"):
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":404,"message":"No such object"}}`)
		return
	case q.Get("upload_id") == "" && q.Get("ifGenerationMatch") != "0":
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":400,"message":"no DoesNotExist condition"}}`)
		return
	}
	_, err := io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		return
	}
	if q.Get("upload_id") == "" {
		f.uploads++
		if f.uploadCode != http.StatusOK {
			w.WriteHeader(f.uploadCode)
			io.WriteString(w, `{"error":{"code":412,"message":"At least one of the pre-conditions you specified did not hold."}}`)
			return
		}
		if q.Get("uploadType") == "resumable" {
			w.Header().Set("Location", "http://"+r.Host+"/upload/session?upload_id=1")
			return
		}
	} else if cr := r.Header.Get("Content-Range"); strings.HasSuffix(cr, "/*") {
		if i := strings.LastIndex(cr, "-"); i >= 0 {
			w.Header().Set("Range", "bytes=0-"+strings.TrimSuffix(cr[i+1:], "/*"))
		}
		w.WriteHeader(http.StatusPermanentRedirect)
		return
	}
	f.stored++
	io.WriteString(w, `{"bucket":"b","name":"replays/C1.pb","generation":"1"}`)
}

func newFakeGCSStore(t *testing.T, f *fakeGCS) *GCSReplayStore {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("STORAGE_EMULATOR_HOST", srv.URL)
	s, err := NewGCSReplayStore(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type failingReader struct {
	r io.Reader
	n int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("client gone")
	}
	n, err := r.r.Read(p[:min(len(p), r.n)])
	r.n -= n
	return n, err
}

func TestGCSReplayStorePut(t *testing.T) {
	const want = "https://storage.googleapis.com/b/replays/C1.pb"
	ctx := context.Background()

	f := &fakeGCS{uploadCode: http.StatusOK}
	if url, err := newFakeGCSStore(t, f).Put(ctx, "replays/C1.pb", strings.NewReader("replay")); err != nil || url != want || f.stored != 1 {
		t.Errorf("upload: %q %v, stored %d", url, err, f.stored)
	}

	// a lost race: 412 on the upload, answered at Close (one chunk) or at Write (an upload of several chunks)
	big := make([]byte, 40<<20)
	big[0], big[1] = 0x1f, 0x8b
	for i := 2; i < len(big); i += 8 {
		v := rand.Uint64()
		for j := 0; j < 8 && i+j < len(big); j++ {
			big[i+j] = byte(v >> (8 * j))
		}
	}
	for name, body := range map[string]string{"close": "replay", "write": string(big)} {
		f := &fakeGCS{uploadCode: http.StatusPreconditionFailed}
		url, err := newFakeGCSStore(t, f).Put(ctx, "replays/C1.pb", strings.NewReader(body))
		if !errors.Is(err, errReplayExists) || url != want || f.uploads == 0 {
			t.Errorf("412 at %s: %q %v, uploads %d", name, url, err, f.uploads)
		}
	}

	// the upload fails midway: nothing is stored
	for name, n := range map[string]int{"first chunk": 1 << 20, "later chunk": 20 << 20} {
		f := &fakeGCS{uploadCode: http.StatusOK}
		_, err := newFakeGCSStore(t, f).Put(ctx, "replays/C1.pb", &failingReader{r: strings.NewReader(string(big)), n: n})
		if err == nil || errors.Is(err, errReplayExists) || f.stored != 0 {
			t.Errorf("reader failure in the %s: %v, stored %d", name, err, f.stored)
		}
	}
}
