// Command dummybackend is a throwaway stand-in backend used for local
// load testing of the reverse proxy. It is not part of the proxy's own
// logic and is not exercised by `go test` — run it as its own process
// so the gateway (cmd/gateway) has something real to route traffic to.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
)

func main() {
	port := flag.String("port", "9001", "port to listen on")
	name := flag.String("name", "backend-1", "identifying name for this instance")
	flag.Parse()

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"secret":  "ok",
			"backend": *name,
		})
	})

	addr := ":" + *port
	log.Printf("dummy backend %s listening on %s", *name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
