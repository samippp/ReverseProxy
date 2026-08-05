package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/db"
	"github.com/samippp/ReverseProxy/proxy"
)

func main() {
	serverConfig := config.ImportConfig()
	env := config.LoadEnv()
	pool := db.PostgreSQLConnectionPoll(env.DatabaseURL)

	fmt.Println("server starting")
	defer pool.Close()
	mux := proxy.NewRouter(pool)
	addr := serverConfig.Server.Hostname + ":" + strconv.Itoa(serverConfig.Server.Port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
