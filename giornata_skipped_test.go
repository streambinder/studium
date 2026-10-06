package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDiarioGiornoSkippedOnlyPiecesGetHomepageCards(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	stmts := []string{
		`INSERT INTO concorsi(id, name, audition_date, weight) VALUES(1, 'Roma', '2026-11-23', 3)`,
		`INSERT INTO pieces(id, composer, work, kind, difficulty) VALUES(1, 'Alfa', 'Pezzo', 'passo', 3)`,
		`INSERT INTO pieces(id, composer, work, kind, difficulty) VALUES(2, 'Beta', 'Pezzo', 'passo', 3)`,
		`INSERT INTO pieces(id, composer, work, kind, difficulty) VALUES(3, 'Gamma', 'Pezzo', 'passo', 3)`,
		`INSERT INTO pieces(id, composer, work, kind, difficulty) VALUES(4, 'Delta', 'Pezzo', 'passo', 3)`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(1, 1)`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(2, 1)`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(3, 1)`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(4, 1)`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 1, 0, 0, 'saltato')`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 2, 0, 0, 'auto-saltato')`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 3, 30, 4, '')`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 3, 0, 0, 'auto-saltato')`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 4, 0, 3, 'baseline')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := &App{db: db}
	req := httptest.NewRequest(http.MethodGet, "/diario/giorno/2026-10-05", nil)
	req.SetPathValue("date", "2026-10-05")
	rec := httptest.NewRecorder()
	a.handleDiarioGiorno(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %.200s", rec.Code, body)
	}
	if !strings.Contains(body, "<h2>Saltati</h2>") {
		t.Fatal("Saltati heading missing")
	}
	if got := strings.Count(body, "skipped-card"); got != 2 {
		t.Fatalf("want 2 skipped cards, got %d", got)
	}
	if got := strings.Count(body, "badge muted done-corner"); got != 2 {
		t.Fatalf("want 2 corner skip badges, got %d", got)
	}
	// Explicit skips come before the plan's own drops.
	ia, ib := strings.Index(body, "Alfa"), strings.Index(body, "Beta")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("explicit skip must precede auto skip: Alfa at %d, Beta at %d", ia, ib)
	}
	// A practiced piece keeps its diary entry even with a skip marker,
	// and a lone valuation stays a diary entry too.
	if !strings.Contains(body, "Gamma") || !strings.Contains(body, "30 min") {
		t.Fatal("practiced piece lost its diary entry")
	}
	if !strings.Contains(body, "Delta") || !strings.Contains(body, "valutazione") {
		t.Fatal("valued piece lost its diary entry")
	}
	if strings.Contains(body, "Nessuna seduta registrata") {
		t.Fatal("empty state shown despite entries and skips")
	}
}

func TestDiarioGiornoOnlySkipsHasNoEmptyState(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	stmts := []string{
		`INSERT INTO concorsi(id, name, audition_date, weight) VALUES(1, 'Roma', '2026-11-23', 3)`,
		`INSERT INTO pieces(id, composer, work, kind, difficulty) VALUES(1, 'Alfa', 'Pezzo', 'passo', 3)`,
		`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(1, 1)`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 1, 0, 0, 'saltato')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := &App{db: db}
	req := httptest.NewRequest(http.MethodGet, "/diario/giorno/2026-10-05", nil)
	req.SetPathValue("date", "2026-10-05")
	rec := httptest.NewRecorder()
	a.handleDiarioGiorno(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %.200s", rec.Code, body)
	}
	if got := strings.Count(body, "skipped-card"); got != 1 {
		t.Fatalf("want 1 skipped card, got %d", got)
	}
	if strings.Contains(body, "Nessuna seduta registrata") {
		t.Fatal("empty state shown on a day with only skips")
	}
}
