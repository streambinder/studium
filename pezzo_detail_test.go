package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// The piece detail page shows today's plan score with its breakdown,
// the piece's rank among today's candidates, and why a piece is out.
func TestPezzoDetailShowsScore(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	today := todayStr()
	future := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	stmts := []string{
		`INSERT INTO concorsi(id, name, audition_date, weight) VALUES(1, 'Santa Cecilia', '` + future + `', 3)`,
		`INSERT INTO pieces(id, composer, work) VALUES(1, 'Strauss', 'Don Juan')`,
		`INSERT INTO pieces(id, composer, work) VALUES(2, 'Bach', 'Suite')`,
		`INSERT INTO pieces(id, composer, work) VALUES(3, 'Mahler', 'Sinfonia')`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(1, 1)`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(3, 1)`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('` + today + `', 3, 0, 0, 'saltato')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /diario/pezzo/{id}", a.handlePezzoDetail)
	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	code, body := get("/diario/pezzo/1")
	if code != http.StatusOK {
		t.Fatalf("piece 1: want 200, got %d", code)
	}
	for _, want := range []string{"Punteggio di oggi", "base 3.0", "Posizione 1 di 1", "tra i primi 8"} {
		if !strings.Contains(body, want) {
			t.Fatalf("piece 1: body missing %q", want)
		}
	}

	code, body = get("/diario/pezzo/2")
	if code != http.StatusOK {
		t.Fatalf("piece 2: want 200, got %d", code)
	}
	if !strings.Contains(body, "Nessun concorso futuro collegato") {
		t.Fatalf("piece 2: want the no-upcoming note")
	}

	code, body = get("/diario/pezzo/3")
	if code != http.StatusOK {
		t.Fatalf("piece 3: want 200, got %d", code)
	}
	if !strings.Contains(body, "Oggi è stato saltato") {
		t.Fatalf("piece 3: want the skipped note")
	}
	if !strings.Contains(body, "Punteggio di oggi") {
		t.Fatalf("piece 3: want the score shown even when skipped")
	}
}
