// Command studium is a daily practice diary for a cellist preparing for
// orchestra auditions: it ranks pieces every day with an explainable score,
// tracks practice time and confidence, and keeps a diary.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
)

type Config struct {
	DataDir  string
	Port     string
	User     string
	Password string
}

func loadConfig() Config {
	cfg := Config{
		DataDir: os.Getenv("STUDIUM_DATA_DIR"),
		Port:    os.Getenv("PORT"),
		User:    os.Getenv("STUDIUM_USER"),
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "/data"
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.User == "" {
		cfg.User = "agnese"
	}
	cfg.Password = os.Getenv("STUDIUM_PASSWORD")
	if cfg.Password == "" {
		log.Fatal("STUDIUM_PASSWORD is required")
	}
	return cfg
}

func main() {
	cfg := loadConfig()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	db, err := openDB(filepath.Join(cfg.DataDir, "studium.db"))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := seedIfEmpty(db); err != nil {
		log.Fatalf("seed: %v", err)
	}
	app := &App{db: db}
	mux := http.NewServeMux()
	app.routes(mux)
	log.Printf("studium in ascolto su :%s (dati in %s)", cfg.Port, cfg.DataDir)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, basicAuth(mux, cfg.User, cfg.Password)))
}
