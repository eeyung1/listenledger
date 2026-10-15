// Command schema applies embedded migrations using a separately supplied administrator URL.
package main

import (
	"log"
	"os"
	"strings"

	"github.com/eeyung1/listenledger-backend/storage"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		log.Fatal("DATABASE_URL must be a PostgreSQL administrator connection URL")
	}
	store, err := storage.OpenPostgres(dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := store.Close(); err != nil {
		log.Fatal(err)
	}
	log.Print("PostgreSQL migrations applied. Apply scripts/supabase_setup.sql before runtime startup.")
}
