package main

import (
	"fmt"
	"log"
	"net/http"
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

	fmt.Println("Gateway starting")
	defer pool.Close()
	mux := proxy.NewRouter(pool, serverConfig, env, 5*time.Second)
	addr := serverConfig.Server.Hostname + ":" + strconv.Itoa(serverConfig.Server.Port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
