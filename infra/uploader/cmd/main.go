package main

import (
	"log"
	"net/http"
	"os"

	// Blank-import the function package so the init() runs
	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	_ "zdxsv/infra/uploader"
)

func main() {
	// Use PORT environment variable, or default to 8080.
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}
	// Local tests: serve UPLOADER_LOCAL_DIR on UPLOADER_LOCAL_ADDR (UPLOADER_LOCAL_URL points there).
	if dir, addr := os.Getenv("UPLOADER_LOCAL_DIR"), os.Getenv("UPLOADER_LOCAL_ADDR"); dir != "" && addr != "" {
		go func() { log.Fatal(http.ListenAndServe(addr, http.FileServer(http.Dir(dir)))) }()
	}
	if err := funcframework.Start(port); err != nil {
		log.Fatalf("funcframework.Start: %v\n", err)
	}
}
