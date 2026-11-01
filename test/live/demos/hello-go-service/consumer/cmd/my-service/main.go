// Command my-service answers GET / with a greeting and GET /health with ok,
// on port 8080.
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// The release build sets these with -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	log.Printf("my-service %s (%s) on :8080", version, commit)

	server := &http.Server{Addr: ":8080", Handler: routes(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "Hello from my-service!")
	})

	return mux
}
