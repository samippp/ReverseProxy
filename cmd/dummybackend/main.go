// Command dummybackend is a stand-in backend for local load testing.
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

	// no catch-all, so a path-stripping bug shows up as a 404
	mux.HandleFunc("/secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"secret":  "ok",
			"backend": *name,
			"path":    r.URL.Path,
		})
	})

	addr := ":" + *port
	log.Printf("dummy backend %s listening on %s", *name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
