// Package proxy handles all http contentsGetDB
package proxy

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samippp/ReverseProxy/api"
	"github.com/samippp/ReverseProxy/auth"
	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/store"
)

func NewRouter(pool *pgxpool.Pool, serverConfig config.ServerConfig, env *config.EnvConfig) *http.ServeMux {
	s := store.New(pool)
	h := api.New(s)
	mux := http.NewServeMux()
	// possibly a silent err if config.yaml does not have secret
	secretBackends := serverConfig.Backends["secret"]
	secretBalancer := NewRoundRobinBalancer(secretBackends)
	secretBalancer.StartHealthChecks(5 * time.Second)
	backendProxy := NewReverseProxy(secretBalancer)
	mux.HandleFunc("/home", h.HomeHandler)
	mux.HandleFunc("/register", h.RegisterHandler)
	mux.HandleFunc("/login", h.Login)
	mux.Handle("/protected/", auth.HandleAuthMiddleware(backendProxy, env.ProxySecret))
	return mux
}
