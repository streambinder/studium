package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSetPriority(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO concorsi(id, name, audition_date, weight) VALUES(1, 'Roma', '2027-01-08', 3)`); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /concorsi/{id}/priorita", a.handleSetPriority)

	post := func(path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("/concorsi/1/priorita", "priority=5"); code != http.StatusNoContent {
		t.Fatalf("set 5: want 204, got %d", code)
	}
	var w int
	if err := db.QueryRow(`SELECT weight FROM concorsi WHERE id=1`).Scan(&w); err != nil {
		t.Fatal(err)
	}
	if w != 5 {
		t.Fatalf("want weight 5, got %d", w)
	}
	if code := post("/concorsi/1/priorita", "priority=99"); code != http.StatusNoContent {
		t.Fatalf("clamp: want 204, got %d", code)
	}
	if err := db.QueryRow(`SELECT weight FROM concorsi WHERE id=1`).Scan(&w); err != nil {
		t.Fatal(err)
	}
	if w != 5 {
		t.Fatalf("clamp: want weight 5, got %d", w)
	}
	if code := post("/concorsi/42/priorita", "priority=2"); code != http.StatusNotFound {
		t.Fatalf("unknown concorso: want 404, got %d", code)
	}
}
