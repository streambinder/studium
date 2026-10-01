package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Prep map: a GitHub-style contribution graph for the homepage.
// Each tile is a piece to prepare. Tile area is proportional to the
// piece's user-calibrated difficulty (1..5); tile color (level 0..4)
// is the preparation state, driven mostly by the user's own confidence
// feedback, then by study time and recency.
type prepTile struct {
	PieceID    int64
	Label      string // "Composer — Work"
	Difficulty int
	Level      int // 0 = no data, 1..4 = preparation state
	Sessions   int
	Minutes    int
	AvgConf    float64 // mean rated confidence, 1..5
	Rated      bool
	Prep       float64 // 0..1 raw preparation score, for sorting
	X, Y, W, H float64 // treemap rect, percent of the map
	Title      string  // tooltip text
	Corner     string  // CSS classes for tiles on the map corners
}

type prepSession struct {
	Date       string // YYYY-MM-DD
	Minutes    int
	Confidence int // 0 = not rated
}

// prepSessions returns all sessions for a piece, newest first.
func (a *App) prepSessions(pieceID int64) ([]prepSession, error) {
	rows, err := a.db.Query(`SELECT date, minutes, confidence FROM sessions
		WHERE piece_id=? ORDER BY date DESC, id DESC`, pieceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prepSession
	for rows.Next() {
		var s prepSession
		if err := rows.Scan(&s.Date, &s.Minutes, &s.Confidence); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// prepScore maps a piece's session history to a 0..1 preparation score.
// The user's confidence feedback weighs most; study volume (scaled by
// difficulty) and recent activity complete the picture.
func prepScore(difficulty int, sessions []prepSession, today time.Time) (score float64, level int, avgConf float64, rated bool) {
	if difficulty < 1 {
		difficulty = 1
	}
	if difficulty > 5 {
		difficulty = 5
	}
	n := len(sessions)
	if n == 0 {
		return 0, 0, 0, false
	}
	minutes := 0
	recent := 0
	cutoff := today.AddDate(0, 0, -21).Format("2006-01-02")
	confSum := 0
	confN := 0
	for i, s := range sessions {
		minutes += s.Minutes
		if s.Date >= cutoff {
			recent++
		}
		if i < 5 && s.Confidence > 0 {
			confSum += s.Confidence
			confN++
		}
	}
	rated = confN > 0
	if rated {
		avgConf = float64(confSum) / float64(confN)
	}
	volNorm := float64(minutes) / float64(difficulty*60) // a difficulty-d piece is "covered" at d hours
	if volNorm > 1 {
		volNorm = 1
	}
	recNorm := float64(recent) / 3
	if recNorm > 1 {
		recNorm = 1
	}
	if rated {
		confNorm := (avgConf - 1) / 4
		score = 0.6*confNorm + 0.25*volNorm + 0.15*recNorm
	} else {
		score = 0.65*volNorm + 0.35*recNorm
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	level = 1 + int(score*3+0.5)
	if level > 4 {
		level = 4
	}
	return score, level, avgConf, rated
}

// prepTiles builds the treemap tiles for every active piece.
func (a *App) prepTiles(todayStr string) ([]prepTile, error) {
	pieces, err := a.listPieces(0, "", false)
	if err != nil {
		return nil, err
	}
	today, err := time.Parse("2006-01-02", todayStr)
	if err != nil {
		return nil, err
	}
	tiles := make([]prepTile, 0, len(pieces))
	for _, p := range pieces {
		sessions, err := a.prepSessions(p.ID)
		if err != nil {
			return nil, err
		}
		score, level, avgConf, rated := prepScore(p.Difficulty, sessions, today)
		minutes := 0
		for _, s := range sessions {
			minutes += s.Minutes
		}
		label := strings.TrimSpace(p.Composer + " — " + p.Work)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\nDifficoltà %d/5 · %d sessioni · %d min", label, p.Difficulty, len(sessions), minutes)
		if rated {
			fmt.Fprintf(&b, " · confidenza %.1f/5", avgConf)
		}
		tiles = append(tiles, prepTile{
			PieceID:    p.ID,
			Label:      label,
			Difficulty: p.Difficulty,
			Level:      level,
			Sessions:   len(sessions),
			Minutes:    minutes,
			AvgConf:    avgConf,
			Rated:      rated,
			Prep:       score,
			Title:      b.String(),
		})
	}
	// Biggest and least-prepared first: the eye lands where work is needed.
	sort.SliceStable(tiles, func(i, j int) bool {
		if tiles[i].Difficulty != tiles[j].Difficulty {
			return tiles[i].Difficulty > tiles[j].Difficulty
		}
		if tiles[i].Prep != tiles[j].Prep {
			return tiles[i].Prep < tiles[j].Prep
		}
		return tiles[i].PieceID < tiles[j].PieceID
	})
	layoutTreemap(tiles, 0, 0, 100, 100, true)
	markCorners(tiles)
	return tiles, nil
}

// markCorners flags the tiles touching the map corners so the template
// can round their outer corner like the panel around the map.
func markCorners(tiles []prepTile) {
	const eps = 0.01
	for i := range tiles {
		t := &tiles[i]
		if t.X < eps && t.Y < eps {
			t.Corner += " ctl"
		}
		if t.X+t.W > 100-eps && t.Y < eps {
			t.Corner += " ctr"
		}
		if t.X < eps && t.Y+t.H > 100-eps {
			t.Corner += " cbl"
		}
		if t.X+t.W > 100-eps && t.Y+t.H > 100-eps {
			t.Corner += " cbr"
		}
	}
}

// layoutTreemap assigns rects with slice-and-dice: alternating splits
// proportional to difficulty, so the map always fills its fixed box.
func layoutTreemap(tiles []prepTile, x, y, w, h float64, horizontal bool) {
	if len(tiles) == 0 {
		return
	}
	if len(tiles) == 1 {
		tiles[0].X, tiles[0].Y, tiles[0].W, tiles[0].H = x, y, w, h
		return
	}
	weights := make([]float64, len(tiles))
	total := 0.0
	for i, t := range tiles {
		d := t.Difficulty
		if d < 1 {
			d = 1
		}
		weights[i] = float64(d)
		total += weights[i]
	}
	// Split where the cumulative weight crosses half.
	split, cum, best := 1, 0.0, total
	for i := 0; i < len(tiles)-1; i++ {
		cum += weights[i]
		if d := abs(cum - total/2); d < best {
			best, split = d, i+1
		}
	}
	left := 0.0
	for i := 0; i < split; i++ {
		left += weights[i]
	}
	if horizontal {
		w1 := w * left / total
		layoutTreemap(tiles[:split], x, y, w1, h, false)
		layoutTreemap(tiles[split:], x+w1, y, w-w1, h, false)
	} else {
		h1 := h * left / total
		layoutTreemap(tiles[:split], x, y, w, h1, true)
		layoutTreemap(tiles[split:], x, y+h1, w, h-h1, true)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
