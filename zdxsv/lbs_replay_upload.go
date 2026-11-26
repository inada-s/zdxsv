package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"go.uber.org/zap"
	"google.golang.org/api/googleapi"
)

// POST /lbs/replay_upload?battle_code=&session_id= (session_id = the battle's ggpo_session, else 403;
// checked before the body is read): an emulator posts a battle's replay as a multipart file
// named <battle_code>.pb (plain or gzip encoded) after the battle. 400 unless it is a replay of that battle
// (checkReplay). It is stored gzip encoded as replays/<battle_code>.pb and its url is recorded on the battle
// (served by /lbs/replay). 409 when that replay is already there and recorded.
// The form field used_ms (JSON, per round, by position: the MS id + 1, 0: unknown, e.g.
// [[3,7,0,12],[3,9,0,12]]; at most the replay's rounds) sets used_ms on the battle's records once the replay is stored.

const (
	replayUploadPrefix  = "replays/"
	replayUploadMaxSize = 64 << 20 // a replay with its start state is ~10 MB
	replayUploadMemory  = 1 << 20
	replayUploadTime    = 5 * time.Minute
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
	// Cancelling wctx before Close aborts the upload: no partial object is created.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(wctx)
	w.ContentEncoding = "gzip"
	w.CacheControl = "no-transform"
	if err := copyGzip(w, r); err != nil {
		cancel()
		w.Close()
		if gcsPreconditionFailed(err) {
			return url, errReplayExists
		}
		return "", err
	}
	if err := w.Close(); err != nil {
		if gcsPreconditionFailed(err) {
			return url, errReplayExists
		}
		return "", fmt.Errorf("writer close: %w", err)
	}
	return url, nil
}

func gcsPreconditionFailed(err error) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == http.StatusPreconditionFailed
}

var errNotReplay = errors.New("not a replay")

// checkReplay reads a whole upload, plain or gzip encoded, and returns its round_data (field 18) count. It fails
// unless the upload decodes to at most replayUploadMaxSize bytes of protobuf fields whose first one is
// battle_code (field 3) = battleCode, the way a replay is written.
func checkReplay(r io.Reader, battleCode string) (int, error) {
	br := bufio.NewReader(r)
	var src io.Reader = br
	if isGzip(br) {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return 0, fmt.Errorf("gzip: %w", err)
		}
		src = zr
	}
	lr := &io.LimitedReader{R: src, N: replayUploadMaxSize + 1}
	rounds, err := walkReplay(bufio.NewReader(lr), battleCode)
	if lr.N <= 0 {
		return 0, fmt.Errorf("decoded size over %d bytes", replayUploadMaxSize)
	}
	return rounds, err
}

func walkReplay(pr *bufio.Reader, battleCode string) (int, error) {
	rounds := 0
	for i := 0; ; i++ {
		tag, err := binary.ReadUvarint(pr)
		if err == io.EOF && i > 0 {
			return rounds, nil
		}
		if err != nil {
			return 0, fmt.Errorf("field %d tag: %w", i, err)
		}
		field, wire := tag>>3, tag&7
		if field == 0 || i == 0 && (field != 3 || wire != 2) {
			return 0, errNotReplay
		}
		switch wire {
		case 0:
			_, err = binary.ReadUvarint(pr)
		case 1:
			_, err = pr.Discard(8)
		case 5:
			_, err = pr.Discard(4)
		case 2:
			var n uint64
			if n, err = binary.ReadUvarint(pr); err != nil {
				break
			}
			if n > replayUploadMaxSize {
				return 0, errNotReplay
			}
			if i == 0 {
				if n != uint64(len(battleCode)) {
					return 0, errNotReplay
				}
				b := make([]byte, n)
				if _, err = io.ReadFull(pr, b); err == nil && string(b) != battleCode {
					return 0, errNotReplay
				}
				break
			}
			_, err = pr.Discard(int(n))
			if field == 18 {
				rounds++
			}
		default:
			return 0, errNotReplay
		}
		if err != nil {
			return 0, fmt.Errorf("field %d: %w", field, err)
		}
	}
}

// isGzip tells whether br starts with the gzip magic 1f 8b; a .pb file never does.
func isGzip(br *bufio.Reader) bool {
	head, _ := br.Peek(2)
	return len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b
}

// copyGzip writes r gzip encoded to w: a gzip stream as it is (a client compressed it), else
// compressed.
func copyGzip(w io.Writer, r io.Reader) error {
	br := bufio.NewReader(r)
	if isGzip(br) {
		if _, err := io.Copy(w, br); err != nil {
			return fmt.Errorf("copy: %w", err)
		}
		return nil
	}
	gw := gzip.NewWriter(w)
	if _, err := io.Copy(gw, br); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}
	return nil
}

// DirReplayStore writes plain files (a gzip upload decoded) under Dir; their urls are URL + name (for local tests,
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
	br := bufio.NewReader(r)
	var src io.Reader = br
	if isGzip(br) {
		zr, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			os.Remove(path)
			return "", err
		}
		src = io.LimitReader(zr, replayUploadMaxSize)
	}
	if _, err := io.Copy(f, src); err != nil {
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
	battleCode := r.URL.Query().Get("battle_code")
	if code, msg := replayUploadAllowed(battleCode, r.URL.Query().Get("session_id")); code != 0 {
		http.Error(w, msg, code)
		return
	}
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(replayUploadTime))
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
	if fh.Filename != battleCode+".pb" {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}
	file, err := fh.Open()
	if err != nil {
		http.Error(w, "Unable to open file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	rounds, err := checkReplay(file, battleCode)
	if err != nil {
		floodLog.Warn("replay_upload: not a replay", zap.Any("battle_code", battleCode), zap.Error(err))
		http.Error(w, "Not a replay", http.StatusBadRequest)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "Unable to read file", http.StatusInternalServerError)
		return
	}
	usedMs, usedMsErr := parseUsedMs(r.MultipartForm.Value["used_ms"], rounds)
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	url, err := h.ReplayStore.Put(ctx, replayUploadPrefix+fh.Filename, file)
	cancel()
	if errors.Is(err, errReplayExists) {
		// stored by an upload whose url was not recorded (a failure, or it is racing this one): record it here
		recorded, rerr := replayRecorded(battleCode)
		if rerr != nil {
			http.Error(w, "Failed to find the battle", http.StatusInternalServerError)
			return
		}
		if recorded {
			http.Error(w, "Already uploaded", http.StatusConflict)
			return
		}
	} else if err != nil {
		logger.Warn("replay_upload: store failure", zap.Any("battle_code", battleCode), zap.Error(err))
		http.Error(w, "Failed to upload file", http.StatusInternalServerError)
		return
	}
	set := h.SetReplayURL
	if set == nil {
		set = DefaultDB.SetReplayURL
	}
	if err := set(battleCode, url); err != nil {
		logger.Warn("replay_upload: SetReplayURL failure", zap.Any("battle_code", battleCode), zap.Error(err))
		http.Error(w, "Failed to record replay", http.StatusInternalServerError)
		return
	}
	if usedMsErr != nil {
		logger.Warn("replay_upload: used_ms not saved", zap.Any("battle_code", battleCode), zap.Error(usedMsErr))
	} else {
		saveUsedMs(battleCode, usedMs)
	}
	logger.Info("replay uploaded", zap.Any("battle_code", battleCode), zap.Int64("size", fh.Size), zap.Any("url", url))
	fmt.Fprintf(w, "File uploaded: %q (%v bytes)\n", fh.Filename, fh.Size)
}

func replayUploadAllowed(battleCode, session string) (int, string) {
	if battleCode == "" || strings.ContainsAny(battleCode, "/\\.") {
		return http.StatusBadRequest, "Invalid battle_code"
	}
	id, err := strconv.ParseInt(session, 10, 64)
	if err != nil || id == 0 {
		return http.StatusForbidden, "Invalid session_id"
	}
	records, err := DefaultDB.GetBattleRecords(battleCode)
	if err != nil {
		logger.Warn("replay_upload: GetBattleRecords failure", zap.Any("battle_code", battleCode), zap.Error(err))
		return http.StatusInternalServerError, "Failed to find the battle"
	}
	if len(records) == 0 || records[0].GgpoSession == 0 || id != int64(records[0].GgpoSession) && id != int64(int32(records[0].GgpoSession)) {
		return http.StatusForbidden, "Unknown battle or session"
	}
	for _, rec := range records {
		if rec.ReplayURL != "" {
			return http.StatusConflict, "Already uploaded"
		}
	}
	return 0, ""
}

func saveUsedMs(battleCode string, rounds [][]int) {
	if len(rounds) == 0 {
		return
	}
	records, err := DefaultDB.GetBattleRecords(battleCode)
	if err != nil {
		logger.Warn("replay_upload: GetBattleRecords failure", zap.Any("battle_code", battleCode), zap.Error(err))
		return
	}
	for _, r := range records {
		var mask uint64
		var list []string
		for _, rd := range rounds {
			ms := 0
			if i := r.Pos - 1; 0 <= i && i < len(rd) {
				ms = rd[i]
			}
			if ms < 0 || 64 <= ms {
				ms = 0
			}
			list = append(list, strconv.Itoa(ms))
			if ms > 0 {
				mask |= 1 << ms
			}
		}
		if err := DefaultDB.SetUsedMs(r.BattleCode, r.UserID, mask, strings.Join(list, ",")); err != nil {
			logger.Warn("replay_upload: SetUsedMs failure", zap.Any("battle_code", battleCode), zap.Error(err))
		}
	}
}

func replayRecorded(battleCode string) (bool, error) {
	records, err := DefaultDB.GetBattleRecords(battleCode)
	if err != nil {
		logger.Warn("replay_upload: GetBattleRecords failure", zap.Any("battle_code", battleCode), zap.Error(err))
		return false, err
	}
	for _, rec := range records {
		if rec.ReplayURL != "" {
			return true, nil
		}
	}
	return false, nil
}

// parseUsedMs takes at most maxRounds rounds (the replay's round results) of at most the most players a rule allows.
func parseUsedMs(values []string, maxRounds int) ([][]int, error) {
	if len(values) == 0 {
		return nil, errors.New("no used_ms field")
	}
	var rounds [][]int
	if err := json.Unmarshal([]byte(values[0]), &rounds); err != nil {
		return nil, fmt.Errorf("used_ms: %w", err)
	}
	if len(rounds) > maxRounds {
		return nil, fmt.Errorf("used_ms of %d rounds, the replay has %d", len(rounds), maxRounds)
	}
	for _, rd := range rounds {
		if len(rd) > maxRulePlayers() {
			return nil, fmt.Errorf("used_ms round of %d players", len(rd))
		}
	}
	return rounds, nil
}

func maxRulePlayers() int {
	n := 0
	for _, e := range ruleList[rulePlayerCount].List {
		n = max(n, int(e.Value))
	}
	return n
}
