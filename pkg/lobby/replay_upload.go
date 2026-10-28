package lobby

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zdxsv/pkg/db"

	"cloud.google.com/go/storage"
	"github.com/golang/glog"
)

// POST /lbs/replay_upload (as gdxsv lbs): pcsx2 posts a battle's replay as a multipart file
// named <battle_code>.pb after the battle. It is stored as replays/<battle_code>.pb and its
// url is recorded on the battle (served by /lbs/replay). 409 when that replay is already there.

const (
	replayUploadPrefix  = "replays/"
	replayUploadMaxSize = 64 << 20 // a pcsx2 replay with its start state is ~10 MB
	replayUploadMemory  = 16 << 20
)

var errReplayExists = errors.New("replay already uploaded")

// ReplayStore keeps uploaded replays.
type ReplayStore interface {
	// Put stores the object unless it exists (errReplayExists) and returns its public url.
	Put(ctx context.Context, name string, r io.Reader) (string, error)
}

// GCSReplayStore writes gzip encoded objects to a public GCS bucket
// (credentials: GOOGLE_APPLICATION_CREDENTIALS or the VM's service account).
type GCSReplayStore struct {
	bucketName string
	bucket     *storage.BucketHandle
}

func NewGCSReplayStore(ctx context.Context, bucketName string) (*GCSReplayStore, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCSReplayStore{bucketName: bucketName, bucket: client.Bucket(bucketName)}, nil
}

func (s *GCSReplayStore) Put(ctx context.Context, name string, r io.Reader) (string, error) {
	obj := s.bucket.Object(name)
	url := "https://storage.googleapis.com/" + s.bucketName + "/" + name
	if _, err := obj.Attrs(ctx); err == nil {
		return url, errReplayExists
	} else if !errors.Is(err, storage.ErrObjectNotExist) {
		return "", fmt.Errorf("attrs: %w", err)
	}

	// DoesNotExist: a second upload racing this one fails instead of overwriting.
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ContentEncoding = "gzip"
	w.CacheControl = "no-transform"
	gw := gzip.NewWriter(w)
	if _, err := io.Copy(gw, r); err != nil {
		w.Close()
		return "", fmt.Errorf("copy: %w", err)
	}
	if err := gw.Close(); err != nil {
		w.Close()
		return "", fmt.Errorf("gzip close: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("writer close: %w", err)
	}
	return url, nil
}

// DirReplayStore writes plain files under Dir; their urls are URL + name (for local tests,
// served by APIHandler at /lbs/replays/).
type DirReplayStore struct {
	Dir string
	URL string
}

func (s *DirReplayStore) Put(_ context.Context, name string, r io.Reader) (string, error) {
	url := s.URL + name
	path := filepath.Join(s.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return url, errReplayExists
	}
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	return url, f.Close()
}

func (h *APIHandler) serveReplayUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if h.ReplayStore == nil {
		http.Error(w, "replay upload is disabled", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, replayUploadMaxSize)
	if err := r.ParseMultipartForm(replayUploadMemory); err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	// the first file of the form, whatever its field name
	var fh *multipart.FileHeader
	for _, headers := range r.MultipartForm.File {
		if len(headers) > 0 {
			fh = headers[0]
			break
		}
	}
	if fh == nil {
		http.Error(w, "No file", http.StatusBadRequest)
		return
	}
	battleCode := strings.TrimSuffix(fh.Filename, ".pb")
	if battleCode == "" || battleCode == fh.Filename || strings.ContainsAny(battleCode, "/\\") {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}
	file, err := fh.Open()
	if err != nil {
		http.Error(w, "Unable to open file", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	url, err := h.ReplayStore.Put(ctx, replayUploadPrefix+fh.Filename, file)
	cancel()
	file.Close()
	if errors.Is(err, errReplayExists) {
		http.Error(w, "Already uploaded", http.StatusConflict)
		return
	}
	if err != nil {
		glog.Warningln("replay_upload: store failure", battleCode, err)
		http.Error(w, "Failed to upload file", http.StatusInternalServerError)
		return
	}
	set := h.SetReplayURL
	if set == nil {
		set = db.DefaultDB.SetReplayURL
	}
	if err := set(battleCode, url); err != nil {
		glog.Warningln("replay_upload: SetReplayURL failure", battleCode, err)
		http.Error(w, "Failed to record replay", http.StatusInternalServerError)
		return
	}
	glog.Infoln("replay uploaded", battleCode, fh.Size, url)
	fmt.Fprintf(w, "File uploaded: %q (%v bytes)\n", fh.Filename, fh.Size)
}
