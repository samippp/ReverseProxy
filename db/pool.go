package db

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samippp/ReverseProxy/config"
)

func PostgreSQLConnectionPoll(databaseURL string) *pgxpool.Pool {
	config.LoadEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Could not open pgxpool: %v", err)
	}
	return pool
}
