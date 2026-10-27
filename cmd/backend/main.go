// Command backend is a legacy standalone backend that checks X-Internal-Secret.
package main

import (
	"log"
	"net/http"

	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/db"
	"github.com/samippp/ReverseProxy/secret"
)

func main() {
	env := config.LoadEnv()

	pool := db.PostgreSQLConnectionPoll(env.DatabaseURL)
	defer pool.Close()

	mux := http.NewServeMux()
	mux.Handle("/protected/secret", internalAuth(secret.Handler(), env.ProxySecret))

	addr := "127.0.0.1:8081" // internal only
	log.Printf("backend listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// internalAuth rejects requests without the expected X-Internal-Secret.
func internalAuth(next http.Handler, expectedSecret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != expectedSecret {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
