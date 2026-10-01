// Command studium is a daily practice diary for a cellist preparing for
// orchestra auditions: it ranks pieces every day with an explainable score,
// tracks practice time and confidence, and keeps a diary.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
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
	healthcheck := flag.Bool("healthcheck", false, "check /healthz on the local server and exit")
	flag.Parse()
	if *healthcheck {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		os.Exit(runHealthcheck(port))
	}
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
	inner := http.NewServeMux()
	app.routes(inner)
	outer := http.NewServeMux()
	outer.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	outer.Handle("/", basicAuth(inner, cfg.User, cfg.Password))
	log.Printf("studium in ascolto su :%s (dati in %s)", cfg.Port, cfg.DataDir)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, outer))
}

// runHealthcheck probes the local /healthz endpoint for container HEALTHCHECK.
func runHealthcheck(port string) int {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: unexpected status", resp.Status)
		return 1
	}
	return 0
}
