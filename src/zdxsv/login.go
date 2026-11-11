package main

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"time"

	"zdxsv/pkg/login"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

var (
	since = time.Now()
)

// assets
func handleAssets(w http.ResponseWriter, r *http.Request) {
	if _, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil {
		h := w.Header()
		delete(h, "Content-Type")
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}

	bin, err := login.Asset(r.URL.Path[1:])
	if err != nil {
		zlog.Error("login server failure", zap.Error(err))
		w.WriteHeader(404)
		return
	}

	if m := mime.TypeByExtension(path.Ext(r.URL.Path)); m != "" {
		w.Header().Set("Content-Type", m)
	}

	w.Header().Set("Cache-Control", "max-age=60, public")
	w.Header().Set("Expires", time.Now().Add(60*time.Second).Format(http.TimeFormat))
	w.Header().Set("Last-Modified", since.UTC().Format(http.TimeFormat))

	w.WriteHeader(200)
	w.Write(bin)
}

// health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf8")
	w.WriteHeader(200)
	fmt.Fprintf(w, "works\n")
}

type wrapper struct {
	http.ResponseWriter
	status int
}

func (r *wrapper) Write(p []byte) (int, error) {
	return r.ResponseWriter.Write(p)
}

func (r *wrapper) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func wrapHandler(f http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := &wrapper{ResponseWriter: w}
		f.ServeHTTP(m, r)

		if r.Method == "POST" {
			r.ParseForm()
		}
		zlog.Info("login http", zap.Any("status", m.status), zap.String("addr", r.RemoteAddr), zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.String("query", r.URL.RawQuery), zap.Any("form", r.Form), zap.String("ua", r.UserAgent()))

	}
}

func newLoginHandler() http.Handler {
	login.Prepare()
	router := http.NewServeMux()
	router.HandleFunc("/health", handleHealth)
	router.HandleFunc("/assets/", handleAssets)
	router.HandleFunc("/CRS-top.jsp", login.HandleTopPage)
	router.HandleFunc("/login", login.HandleLoginPage)
	router.HandleFunc("/register", login.HandleRegisterPage)
	return wrapHandler(router)
}

// assetsCheck fails when the embedded assets are missing.
func assetsCheck() ([]byte, error) {
	return login.Asset("assets/checkfile")
}
