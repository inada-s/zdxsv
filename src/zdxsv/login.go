package main

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"time"

	"zdxsv/pkg/assets"
	"zdxsv/pkg/config"
	"zdxsv/pkg/login"

	"github.com/golang/glog"
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

	bin, err := assets.Asset(r.URL.Path[1:])
	if err != nil {
		glog.Errorln(err)
		w.WriteHeader(404)
		return
	}

	if m := mime.TypeByExtension(path.Ext(r.URL.Path)); m != "" {
		w.Header().Set("Content-Type", m)
	}

	w.Header().Set("Cache-Control", "max-age:60, public")
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
		glog.Infoln(
			m.status,
			r.RemoteAddr,
			r.Method,
			r.URL.Path,
			r.URL.RawQuery,
			r.Form,
			r.UserAgent(),
		)

	}
}

func mainLogin() {
	if _, err := assets.Asset("assets/checkfile"); err != nil {
		glog.Fatalln(err)
	}
	login.Prepare()
	router := http.NewServeMux()
	router.HandleFunc("/health", handleHealth)
	router.HandleFunc("/assets/", handleAssets)
	router.HandleFunc("/CRS-top.jsp", login.HandleTopPage)
	// router.HandleFunc("/CRS-top.jsp", login.HandleTestPage)
	router.HandleFunc("/login", login.HandleLoginPage)
	router.HandleFunc("/register", login.HandleRegisterPage)
	err := http.ListenAndServe(stripHost(config.Conf.Login.Addr), wrapHandler(router))
	if err != nil {
		glog.Fatalln(err)
	}
}
