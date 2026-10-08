// Package function is the replay uploader Cloud Function (a copy of gdxsv infra/uploader).
// pcsx2 posts a battle's replay (<battle_code>.pb, multipart field "file"); it is stored
// gzip-encoded under replays/ and the lobby is told its url (/ops/replay_uploaded).
//
// Settings (environment):
//
//	UPLOADER_BUCKET     GCS bucket (default zdxsv)
//	UPLOADER_LOBBY_URL  the lobby's /ops/replay_uploaded url, no notice when empty
//	UPLOADER_LOCAL_DIR  store under this directory instead of GCS (local tests)
//	UPLOADER_LOCAL_URL  public url of UPLOADER_LOCAL_DIR, used in the lobby notice
package function

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

func init() {
	functions.HTTP("FunctionEntryPoint", uploadHandler)
}

var (
	bucketName     = envOr("UPLOADER_BUCKET", "zdxsv")
	uploadBasePath = "replays/"
	lobbyURL       = os.Getenv("UPLOADER_LOBBY_URL")
	localDir       = os.Getenv("UPLOADER_LOCAL_DIR")
	localURL       = strings.TrimSuffix(os.Getenv("UPLOADER_LOCAL_URL"), "/")
	ErrExist       = os.ErrExist
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fileExists(ctx context.Context, bucketName, objectName string) (bool, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return false, fmt.Errorf("storage.NewClient: %v", err)
	}
	defer client.Close()

	bucket := client.Bucket(bucketName)
	_, err = bucket.Object(objectName).Attrs(ctx)
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("Object(%q).Attrs: %v", objectName, err)
	}
	return true, nil
}

func uploadFileToGCS(ctx context.Context, bucketName, objectName string, r io.Reader) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("storage.NewClient: %v", err)
	}
	defer client.Close()

	bucket := client.Bucket(bucketName)
	obj := bucket.Object(objectName)

	// Do not upload when the same file already exists.
	exists, err := fileExists(ctx, bucketName, objectName)
	if err != nil {
		return fmt.Errorf("fileExists: %v", err)
	}
	if exists {
		return ErrExist
	}

	w := obj.NewWriter(ctx)
	w.ContentEncoding = "gzip"
	w.CacheControl = "no-transform"
	gw := gzip.NewWriter(w)

	if _, err = io.Copy(gw, r); err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("gzipWriter.Close: %v", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("Writer.Close: %v", err)
	}

	return nil
}

// uploadFileToDir stores the file as it is (no gzip: any static file server can serve it).
func uploadFileToDir(dir, objectName string, r io.Reader) error {
	path := filepath.Join(dir, filepath.FromSlash(objectName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return ErrExist
	}
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("io.Copy: %v", err)
	}
	return f.Close()
}

func notifyReplayUploadedToLobby(battleCode string, uploadedURL string) {
	if lobbyURL == "" {
		return
	}
	req, err := http.NewRequest("GET", lobbyURL, nil)
	if err != nil {
		log.Print("NewRequest failure:", err)
		return
	}

	q := req.URL.Query()
	q.Add("battle_code", battleCode)
	q.Add("url", uploadedURL)
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Print("DefaultClient.Do failure:", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Print("error while reading response of replay_uploaded")
		return
	}

	log.Print(resp.StatusCode, " ", string(body))
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 10 * 1024 * 1024 // 10 megabytes.
	ctx := context.Background()

	// Up to maxMemory bytes of the file parts are kept in memory, the rest in
	// temporary files (a pcsx2 replay with its start state is ~10 MB).
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		log.Printf("Error parsing form: %v", err)
		return
	}

	// Be sure to remove all temporary files after your function is finished.
	defer func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			log.Printf("Error cleaning up form files: %v", err)
		}
	}()

	for _, headers := range r.MultipartForm.File {
		for _, h := range headers {
			battleCode, ok := strings.CutSuffix(h.Filename, ".pb")
			if !ok || battleCode == "" || strings.ContainsAny(battleCode, `/\.`) {
				http.Error(w, "Invalid file name", http.StatusBadRequest)
				return
			}

			file, err := h.Open()
			if err != nil {
				http.Error(w, "Unable to open file", http.StatusBadRequest)
				return
			}

			objectName := uploadBasePath + h.Filename
			uploadedURL := "https://storage.googleapis.com/" + bucketName + "/" + objectName
			if localDir != "" {
				err = uploadFileToDir(localDir, objectName, file)
				uploadedURL = localURL + "/" + objectName
			} else {
				err = uploadFileToGCS(ctx, bucketName, objectName, file)
			}
			file.Close()
			if err != nil && err != ErrExist {
				log.Printf("upload %s: %v", objectName, err)
				http.Error(w, "Failed to upload file", http.StatusInternalServerError)
				return
			}

			// Also on a re-upload: the lobby notice is idempotent and may have failed before.
			notifyReplayUploadedToLobby(battleCode, uploadedURL)

			if err == ErrExist {
				http.Error(w, "Already uploaded", http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "File uploaded: %q (%v bytes)\n", h.Filename, h.Size)
			return
		}
	}

	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "No file")
}
