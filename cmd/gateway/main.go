// Command gateway runs the API gateway.
package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"time"

	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/db"
	"github.com/samippp/ReverseProxy/proxy"
)

func main() {
	serverConfig := config.ImportServerConfig()
	env := config.LoadEnv()
	pool := db.PostgreSQLConnectionPoll(env.DatabaseURL)

	// pprof uses DefaultServeMux, which the router doesn't, so it's only on this port
	if addr := os.Getenv("PPROF_ADDR"); addr != "" {
		go func() {
			log.Printf("pprof listening on %s", addr)
			log.Println(http.ListenAndServe(addr, nil))
		}()
	}

	fmt.Println("Gateway starting")
	defer pool.Close()
	mux := proxy.NewRouter(pool, serverConfig, env, 5*time.Second)
	addr := serverConfig.Server.Hostname + ":" + strconv.Itoa(serverConfig.Server.Port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
