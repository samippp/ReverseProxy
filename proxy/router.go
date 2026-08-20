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

func NewRouter(pool *pgxpool.Pool, serverConfig config.ServerConfig, env *config.EnvConfig, healthCheckInterval time.Duration) http.Handler {
	s := store.New(pool)
	h := api.New(s)
	mux := http.NewServeMux()

	secretBackends := serverConfig.Backends["secret"]
	secretBalancer := NewRoundRobinBalancer(secretBackends)
	secretBalancer.StartHealthChecks(healthCheckInterval)
	backendProxy := NewReverseProxy(secretBalancer, "/protected")

	mux.HandleFunc("/home", h.HomeHandler)
	mux.HandleFunc("/register", h.RegisterHandler)
	mux.HandleFunc("/login", h.Login)
	mux.Handle("/protected/", auth.HandleAuthMiddleware(backendProxy, env.ProxySecret))

	return LoggingMiddleware(mux)
}
