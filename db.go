package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

type Concorso struct {
	ID       int64
	Name     string
	Date     string // YYYY-MM-DD
	Weight   int
	Archived bool
	Estratto string  // per-link excerpt, set only when read through piece_concorso
	Level    int     // mean preparation level 0..4 of its pieces, stamped on demand
	Prep     float64 // mean preparation score 0..1 of its pieces, stamped on demand
}

// Piece kinds.
const (
	kindPasso = "passo"
	kindSolo  = "solo"
)

type Piece struct {
	ID         int64
	Composer   string
	Work       string
	Movement   string
	Kind       string // 'passo' | 'solo'
	Difficulty int    // 1..5, user-calibrated; drives the prep-map tile size
	Archived   bool
	Concorsi   []Concorso
	HasActive  bool    // at least one non-archived concorso with date >= today
	Level      int     // preparation level 0..4, stamped on demand (not persisted)
	Prep       float64 // preparation score 0..1, stamped on demand (not persisted)
}

type Session struct {
	ID         int64
	Date       string
	PieceID    int64
	Minutes    int
	Confidence int
	Note       string
	Tempo      sql.NullInt64 // optional metronome mark (BPM); NULL when unrecorded
}

type Availability struct {
	ID       int64
	Date     string
	StartMin int
	EndMin   int
	Label    string
	Kind     string // 'free' | 'busy'
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS concorsi(
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			city TEXT NOT NULL DEFAULT '',
			audition_date TEXT NOT NULL,
			weight INTEGER NOT NULL DEFAULT 1,
			archived_at TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,
		`CREATE TABLE IF NOT EXISTS pieces(
			id INTEGER PRIMARY KEY,
			composer TEXT NOT NULL,
			work TEXT NOT NULL,
			movement TEXT NOT NULL DEFAULT '',
			excerpt TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'passo',
			archived_at TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,
		`CREATE TABLE IF NOT EXISTS piece_concorso(
			piece_id INTEGER NOT NULL REFERENCES pieces(id) ON DELETE CASCADE,
			concorso_id INTEGER NOT NULL REFERENCES concorsi(id) ON DELETE CASCADE,
			PRIMARY KEY(piece_id, concorso_id)
		)`,
		`CREATE TABLE IF NOT EXISTS concorso_links(
			id INTEGER PRIMARY KEY,
			concorso_id INTEGER NOT NULL REFERENCES concorsi(id) ON DELETE CASCADE,
			label TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions(
			id INTEGER PRIMARY KEY,
			date TEXT NOT NULL,
			piece_id INTEGER NOT NULL REFERENCES pieces(id) ON DELETE CASCADE,
			minutes INTEGER NOT NULL DEFAULT 0,
			confidence INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_piece_date ON sessions(piece_id, date)`,
		`CREATE TABLE IF NOT EXISTS availability(
			id INTEGER PRIMARY KEY,
			date TEXT NOT NULL,
			start_min INTEGER NOT NULL,
			end_min INTEGER NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'free',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_availability_date ON availability(date)`,
		`CREATE TABLE IF NOT EXISTS settings(
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// concorsi.archived_at was added after the first schema revision.
	if _, err := db.Exec(`ALTER TABLE concorsi ADD COLUMN archived_at TEXT`); err != nil {
		// duplicate column means the migration already ran; anything else is real.
		if !isDupColumnErr(err) {
			return fmt.Errorf("migrate archived_at: %w", err)
		}
	}
	// pieces.difficulty was added after the first schema revision.
	if _, err := db.Exec(`ALTER TABLE pieces ADD COLUMN difficulty INTEGER NOT NULL DEFAULT 3`); err != nil {
		// duplicate column means the migration already ran; anything else is real.
		if !isDupColumnErr(err) {
			return fmt.Errorf("migrate difficulty: %w", err)
		}
	}
	// piece_concorso.estratto holds the excerpt each concorso asks for; the
	// column started out as notes and is renamed when present.
	if err := ensureLinkEstratto(db); err != nil {
		return err
	}
	// sessions.tempo holds the optional metronome mark (BPM) of a practice
	// session; NULL when the player did not record one.
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN tempo INTEGER`); err != nil {
		// duplicate column means the migration already ran; anything else is real.
		if !isDupColumnErr(err) {
			return fmt.Errorf("migrate tempo: %w", err)
		}
	}
	return nil
}

// ensureLinkEstratto renames the original notes column to estratto, or adds
// estratto on installs that never had notes.
func ensureLinkEstratto(db *sql.DB) error {
	cols, err := tableColumns(db, "piece_concorso")
	if err != nil {
		return fmt.Errorf("migrate piece_concorso estratto: %w", err)
	}
	switch {
	case cols["estratto"]:
		return nil
	case cols["notes"]:
		if _, err := db.Exec(`ALTER TABLE piece_concorso RENAME COLUMN notes TO estratto`); err != nil {
			return fmt.Errorf("migrate piece_concorso estratto: %w", err)
		}
	default:
		if _, err := db.Exec(`ALTER TABLE piece_concorso ADD COLUMN estratto TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate piece_concorso estratto: %w", err)
		}
	}
	return nil
}

// tableColumns returns the column names of a table.
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("rows close: %v", cerr)
		}
	}()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func isDupColumnErr(err error) bool {
	return err != nil && contains(err.Error(), "duplicate column name")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}

func todayStr() string { return time.Now().Format("2006-01-02") }

// daysBetween returns (b - a) in whole days for YYYY-MM-DD strings.
func daysBetween(a, b string) (int, error) {
	ta, err := time.Parse("2006-01-02", a)
	if err != nil {
		return 0, err
	}
	tb, err := time.Parse("2006-01-02", b)
	if err != nil {
		return 0, err
	}
	return int(tb.Sub(ta).Hours() / 24), nil
}

// scanConcorsi reads all concorsi from rows and closes them.
func scanConcorsi(rows *sql.Rows) ([]Concorso, error) {
	return collect(rows, func(rows *sql.Rows) (Concorso, error) {
		var c Concorso
		err := rows.Scan(&c.ID, &c.Name, &c.Date, &c.Weight, &c.Archived)
		return c, err
	})
}

func (a *App) listConcorsi(includeArchived bool) ([]Concorso, error) {
	q := `SELECT id, name, audition_date, weight, archived_at IS NOT NULL
		FROM concorsi`
	if !includeArchived {
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY archived_at IS NOT NULL, audition_date`
	rows, err := a.db.Query(q)
	if err != nil {
		return nil, err
	}
	return scanConcorsi(rows)
}

func (a *App) pieceConcorsi(pieceID int64) ([]Concorso, error) {
	rows, err := a.db.Query(`SELECT c.id, c.name, c.audition_date, c.weight, c.archived_at IS NOT NULL, pc.estratto
		FROM concorsi c JOIN piece_concorso pc ON pc.concorso_id=c.id
		WHERE pc.piece_id=? ORDER BY c.audition_date`, pieceID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(rows *sql.Rows) (Concorso, error) {
		var c Concorso
		err := rows.Scan(&c.ID, &c.Name, &c.Date, &c.Weight, &c.Archived, &c.Estratto)
		return c, err
	})
}

// ConcorsoLink is one external resource attached to a concorso (official
// notice, orchestral parts PDF, ...).
type ConcorsoLink struct {
	ID         int64
	ConcorsoID int64
	Label      string
	URL        string
}

func (a *App) concorsoLinks(concorsoID int64) ([]ConcorsoLink, error) {
	rows, err := a.db.Query(`SELECT id, concorso_id, label, url FROM concorso_links
		WHERE concorso_id=? ORDER BY id`, concorsoID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(rows *sql.Rows) (ConcorsoLink, error) {
		var l ConcorsoLink
		err := rows.Scan(&l.ID, &l.ConcorsoID, &l.Label, &l.URL)
		return l, err
	})
}

func (a *App) addConcorsoLink(concorsoID int64, label, url string) error {
	_, err := a.db.Exec(`INSERT INTO concorso_links(concorso_id, label, url) VALUES(?,?,?)`,
		concorsoID, label, url)
	return err
}

func (a *App) deleteConcorsoLink(concorsoID, linkID int64) error {
	_, err := a.db.Exec(`DELETE FROM concorso_links WHERE id=? AND concorso_id=?`, linkID, concorsoID)
	return err
}

// closeRows checks rows.Err, closes rows (logging close failures) and
// reports the iteration error, if any.
func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); cerr != nil {
		log.Printf("rows close: %v", cerr)
	}
	return err
}

// closeRowsErr closes rows after a scan failure, reporting the scan error
// unless closing surfaced an iteration error first.
func closeRowsErr(rows *sql.Rows, err error) error {
	if rerr := closeRows(rows); rerr != nil {
		return rerr
	}
	return err
}

// collect scans every row with scan, then closes rows. Concorsi are fetched
// by the caller afterwards: the pool is limited to a single connection and
// nested queries would deadlock.
func collect[T any](rows *sql.Rows, scan func(*sql.Rows) (T, error)) ([]T, error) {
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, closeRowsErr(rows, err)
		}
		out = append(out, v)
	}
	return out, closeRows(rows)
}

// scanPieces reads all pieces from rows and closes them.
func scanPieces(rows *sql.Rows) ([]Piece, error) {
	return collect(rows, func(rows *sql.Rows) (Piece, error) {
		var p Piece
		err := rows.Scan(&p.ID, &p.Composer, &p.Work, &p.Movement, &p.Kind, &p.Difficulty, &p.Archived)
		return p, err
	})
}

func (a *App) listPieces(concorsoID int64, kind string, includeArchived bool) ([]Piece, error) {
	q := `SELECT DISTINCT p.id, p.composer, p.work, p.movement, p.kind, p.difficulty,
		p.archived_at IS NOT NULL FROM pieces p`
	args := []any{}
	where := ""
	if concorsoID > 0 {
		q += ` JOIN piece_concorso pc ON pc.piece_id=p.id`
		where += ` AND pc.concorso_id=?`
		args = append(args, concorsoID)
	}
	if kind == kindPasso || kind == kindSolo {
		where += ` AND p.kind=?`
		args = append(args, kind)
	}
	if !includeArchived {
		where += ` AND p.archived_at IS NULL`
	}
	q += ` WHERE 1=1` + where + ` ORDER BY p.composer, p.work, p.movement`
	rows, err := a.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	out, err := scanPieces(rows)
	if err != nil {
		return nil, err
	}
	today := todayStr()
	for i := range out {
		cs, err := a.pieceConcorsi(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Concorsi = cs
		for _, c := range cs {
			if !c.Archived && c.Date >= today {
				out[i].HasActive = true
				break
			}
		}
	}
	return out, nil
}

func (a *App) getPiece(id int64) (Piece, error) {
	var p Piece
	err := a.db.QueryRow(`SELECT id, composer, work, movement, kind, difficulty, archived_at IS NOT NULL
		FROM pieces WHERE id=?`, id).Scan(&p.ID, &p.Composer, &p.Work, &p.Movement, &p.Kind, &p.Difficulty, &p.Archived)
	if err != nil {
		return p, err
	}
	cs, err := a.pieceConcorsi(id)
	if err != nil {
		return p, err
	}
	p.Concorsi = cs
	return p, nil
}

// latestConfidence returns the most recent declared confidence (0..5).
// A baseline declaration of 0 (mai toccato) counts as rated; rated=false
// only when the piece has no valuation at all.
func (a *App) latestConfidence(pieceID int64) (conf float64, rated bool, err error) {
	var c sql.NullInt64
	err = a.db.QueryRow(`SELECT confidence FROM sessions
		WHERE piece_id=? AND (confidence>0 OR note='baseline') ORDER BY date DESC, id DESC LIMIT 1`, pieceID).Scan(&c)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return float64(c.Int64), true, nil
}

// daysSincePractice returns days since the last session with minutes>0;
// ok=false when the piece was never practiced (caller uses the default of 30).
func (a *App) daysSincePractice(pieceID int64, today string) (days int, ok bool, err error) {
	var d sql.NullString
	err = a.db.QueryRow(`SELECT MAX(date) FROM sessions WHERE piece_id=? AND minutes>0`, pieceID).Scan(&d)
	if err != nil {
		return 0, false, err
	}
	if !d.Valid {
		return 0, false, nil
	}
	n, err := daysBetween(d.String, today)
	if err != nil {
		return 0, false, err
	}
	if n < 0 {
		n = 0
	}
	return n, true, nil
}

type todayMark struct {
	Logged    int
	Skipped   bool // a 'saltato' marker exists for today
	Practiced bool // a session with real study time (minutes>0) exists for today
}

// todayMark summarizes today's sessions for a piece.
func (a *App) todayMark(pieceID int64, today string) (todayMark, error) {
	var m todayMark
	rows, err := a.db.Query(`SELECT minutes, confidence, note FROM sessions WHERE piece_id=? AND date=?`, pieceID, today)
	if err != nil {
		return m, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("rows close: %v", err)
		}
	}()
	for rows.Next() {
		var minutes, confidence int
		var note string
		if err := rows.Scan(&minutes, &confidence, &note); err != nil {
			return m, err
		}
		m.Logged += minutes
		if minutes > 0 {
			m.Practiced = true
		}
		// 'rimandato' is honored only for markers written before the
		// postpone feature was removed: they skip the piece for today.
		if note == "saltato" || note == "rimandato" {
			m.Skipped = true
		}
	}
	return m, rows.Err()
}

func (a *App) availabilityFor(date string) ([]Availability, error) {
	rows, err := a.db.Query(`SELECT id, date, start_min, end_min, label, kind FROM availability
		WHERE date=? ORDER BY start_min`, date)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("rows close: %v", err)
		}
	}()
	var out []Availability
	for rows.Next() {
		var v Availability
		if err := rows.Scan(&v.ID, &v.Date, &v.StartMin, &v.EndMin, &v.Label, &v.Kind); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (a *App) getCoeffs() (Coeffs, error) {
	c := DefaultCoeffs()
	vals := map[string]*float64{
		"urgency_k":       &c.UrgencyK,
		"urgency_horizon": &c.UrgencyHorizon,
		"recency_cap":     &c.RecencyCap,
	}
	for k, p := range vals {
		var s string
		err := a.db.QueryRow(`SELECT value FROM settings WHERE key=?`, k).Scan(&s)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return c, err
		}
		var f float64
		if _, err := fmt.Sscanf(s, "%f", &f); err == nil && f > 0 {
			*p = f
		}
	}
	return c, nil
}

func (a *App) setCoeff(key string, v float64) error {
	_, err := a.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, fmt.Sprintf("%g", v))
	return err
}
