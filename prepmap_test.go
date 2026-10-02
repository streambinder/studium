package main

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func mkSessions(specs ...[3]any) []prepSession {
	var out []prepSession
	for _, s := range specs {
		out = append(out, prepSession{Date: s[0].(string), Minutes: s[1].(int), Confidence: s[2].(int)})
	}
	return out
}

func TestPrepScoreNoSessions(t *testing.T) {
	score, level, _, rated := prepScore(3, nil, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if rated || score != 0 || level != 0 {
		t.Fatalf("no sessions: got score=%v level=%d rated=%v", score, level, rated)
	}
}

func TestPrepScoreConfidenceDominates(t *testing.T) {
	today := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// Same volume, different user feedback: confidence must drive the level.
	low := mkSessions([3]any{"2026-09-28", 60, 1}, [3]any{"2026-09-29", 60, 2})
	high := mkSessions([3]any{"2026-09-28", 60, 5}, [3]any{"2026-09-29", 60, 4})
	_, lowLv, _, _ := prepScore(3, low, today)
	_, highLv, _, _ := prepScore(3, high, today)
	if highLv <= lowLv {
		t.Fatalf("confidence should dominate: low=%d high=%d", lowLv, highLv)
	}
}

func TestPrepScoreUnratedFallsBackToVolume(t *testing.T) {
	today := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	none := mkSessions([3]any{"2026-09-29", 30, 0})
	lots := mkSessions(
		[3]any{"2026-09-25", 60, 0},
		[3]any{"2026-09-27", 60, 0},
		[3]any{"2026-09-29", 60, 0},
		[3]any{"2026-09-30", 60, 0},
	)
	_, lvNone, _, rated := prepScore(3, none, today)
	_, lvLots, _, _ := prepScore(3, lots, today)
	if rated {
		t.Fatal("unrated sessions should not count as rated")
	}
	if lvLots <= lvNone {
		t.Fatalf("volume should matter when unrated: none=%d lots=%d", lvNone, lvLots)
	}
}

func TestPrepScoreDifficultyScalesVolume(t *testing.T) {
	today := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := mkSessions([3]any{"2026-09-29", 120, 0})
	_, easyLv, _, _ := prepScore(1, s, today)
	_, hardLv, _, _ := prepScore(5, s, today)
	if easyLv <= hardLv {
		t.Fatalf("same minutes should prepare an easy piece more: easy=%d hard=%d", easyLv, hardLv)
	}
}

func TestPrepScoreBounds(t *testing.T) {
	today := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := mkSessions([3]any{"2026-09-01", 9999, 5}, [3]any{"2026-09-30", 9999, 5})
	score, level, avg, rated := prepScore(0, s, today) // difficulty 0 clamps to 1
	if !rated || avg != 5 {
		t.Fatalf("expected rated avg 5, got rated=%v avg=%v", rated, avg)
	}
	if score < 0 || score > 1 || level < 1 || level > 4 {
		t.Fatalf("out of bounds: score=%v level=%d", score, level)
	}
}

func TestTreemapFillsBox(t *testing.T) {
	tiles := []prepTile{
		{Difficulty: 5}, {Difficulty: 3}, {Difficulty: 4}, {Difficulty: 1}, {Difficulty: 2},
	}
	layoutTreemap(tiles, 0, 0, 100, 100, prepBiasDesktop)
	area := 0.0
	for _, tl := range tiles {
		if tl.W <= 0 || tl.H <= 0 {
			t.Fatalf("non-positive tile: %+v", tl)
		}
		if tl.X < 0 || tl.Y < 0 || tl.X+tl.W > 100.001 || tl.Y+tl.H > 100.001 {
			t.Fatalf("tile out of bounds: %+v", tl)
		}
		area += tl.W * tl.H
	}
	if area < 9999.9 || area > 10000.1 {
		t.Fatalf("tiles should fill the box, total area=%v", area)
	}
	// Bigger difficulty should get at least as much area.
	areas := map[int]float64{}
	for _, tl := range tiles {
		areas[tl.Difficulty] = tl.W * tl.H
	}
	if !(areas[5] > areas[4] && areas[4] > areas[3] && areas[3] > areas[2] && areas[2] > areas[1]) {
		t.Fatalf("area should grow with difficulty: %v", areas)
	}
	// Weights grow faster than linear so size differences read clearly.
	if areas[5] < 8*areas[1] {
		t.Fatalf("hardest piece should dwarf the easiest: %v", areas)
	}
}

func TestTreemapSingleAndEmpty(t *testing.T) {
	layoutTreemap(nil, 0, 0, 100, 100, prepBiasDesktop) // must not panic
	one := []prepTile{{Difficulty: 3}}
	layoutTreemap(one, 0, 0, 100, 100, prepBiasDesktop)
	if one[0].X != 0 || one[0].Y != 0 || one[0].W != 100 || one[0].H != 100 {
		t.Fatalf("single tile should fill the box: %+v", one[0])
	}
}

func TestMigrateDifficultyIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("first migrate failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pieces(composer, work) VALUES('Bach','Suite')`); err != nil {
		t.Fatal(err)
	}
	var d int
	if err := db.QueryRow(`SELECT difficulty FROM pieces`).Scan(&d); err != nil {
		t.Fatalf("difficulty column missing after migrate: %v", err)
	}
	if d != 3 {
		t.Fatalf("expected default difficulty 3, got %d", d)
	}
	// Running migrate again must be a no-op, not an error.
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE pieces SET difficulty=5`); err != nil {
		t.Fatalf("difficulty not writable: %v", err)
	}
}
