package main

import (
	"log"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"

	_ "github.com/inada-s/zdxsv/infra/udptest"
)

func main() {
	port := "8080"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}
	if err := funcframework.Start(port); err != nil {
		log.Fatalf("funcframework.Start: %v", err)
	}
}
