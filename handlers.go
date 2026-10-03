package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type App struct {
	db *sql.DB
}

func (a *App) routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /{$}", a.handleToday)
	mux.HandleFunc("POST /disponibilita", a.handleAddAvailability)
	mux.HandleFunc("POST /disponibilita/elimina", a.handleDelAvailability)
	mux.HandleFunc("POST /sessione", a.handleSession)
	mux.HandleFunc("POST /sessione/salta", a.handleSkip)
	mux.HandleFunc("GET /pezzi", a.handlePezzi)
	mux.HandleFunc("POST /pezzi", a.handleAddPiece)
	mux.HandleFunc("GET /pezzi/{id}/modifica", a.handleEditPiece)
	mux.HandleFunc("POST /pezzi/{id}/modifica", a.handleUpdatePiece)
	mux.HandleFunc("POST /pezzi/{id}/difficolta", a.handleSetDifficulty)
	mux.HandleFunc("POST /concorsi/{id}/priorita", a.handleSetPriority)
	mux.HandleFunc("POST /pezzi/{id}/archivia", a.handleArchivePiece)
	mux.HandleFunc("POST /pezzi/{id}/ripristina", a.handleRestorePiece)
	mux.HandleFunc("GET /concorsi", a.handleConcorsi)
	mux.HandleFunc("POST /concorsi", a.handleAddConcorso)
	mux.HandleFunc("GET /concorsi/{id}", a.handleConcorsoDetail)
	mux.HandleFunc("GET /concorsi/{id}/modifica", a.handleConcorsoEdit)
	mux.HandleFunc("POST /concorsi/{id}", a.handleUpdateConcorso)
	mux.HandleFunc("POST /concorsi/{id}/link", a.handleAddConcorsoLink)
	mux.HandleFunc("POST /concorsi/{id}/link/{linkID}/elimina", a.handleDeleteConcorsoLink)
	mux.HandleFunc("POST /concorsi/{id}/archivia", a.handleArchiveConcorso)
	mux.HandleFunc("POST /concorsi/{id}/ripristina", a.handleRestoreConcorso)
	mux.HandleFunc("GET /diario", a.handleDiario)
	mux.HandleFunc("GET /diario/giorno/{date}", a.handleDiarioGiorno)
	mux.HandleFunc("GET /diario/pezzo/{id}", a.handlePezzoDetail)
	mux.HandleFunc("GET /impostazioni", a.handleImpostazioni)
	mux.HandleFunc("POST /impostazioni", a.handleSaveImpostazioni)
}

// ---------- helpers ----------

func formInt(r *http.Request, key string, def int) int {
	s := r.FormValue(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func formIDs(r *http.Request, key string) []int64 {
	var out []int64
	for _, s := range r.Form[key] {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func parseHHMM(s string) (int, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func pathID(r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n, err == nil && n > 0
}

// formID extracts the id from the URL path and parses the form body,
// answering the error itself when either fails.
func formID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return 0, false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// pieceOr404 loads the piece named by the URL path, answering 404 itself
// when the id is invalid or the piece does not exist.
func (a *App) pieceOr404(w http.ResponseWriter, r *http.Request) (Piece, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return Piece{}, false
	}
	p, err := a.getPiece(id)
	if err != nil {
		http.NotFound(w, r)
		return Piece{}, false
	}
	return p, true
}

// concorsoNameDate validates the mandatory concorso form fields, answering
// 400 itself when the name or the date is missing.
func concorsoNameDate(w http.ResponseWriter, r *http.Request) (name, date string, ok bool) {
	name = strings.TrimSpace(r.FormValue("name"))
	date = r.FormValue("date")
	if name == "" || date == "" {
		http.Error(w, "nome e data sono obbligatori", http.StatusBadRequest)
		return "", "", false
	}
	return name, date, true
}

// concorsiOr500 lists the active concorsi, answering 500 itself on failure.
func (a *App) concorsiOr500(w http.ResponseWriter) ([]Concorso, bool) {
	concorsi, err := a.listConcorsi(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return concorsi, true
}

// queryRows runs a query, answering 500 itself on failure. The caller must
// close the returned rows, e.g. via defer closeRowsLogged(rows).
func (a *App) queryRows(w http.ResponseWriter, query string, args ...any) (*sql.Rows, bool) {
	rows, err := a.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return rows, true
}

// closeRowsLogged closes rows, logging failures. Use with defer.
func closeRowsLogged(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("rows close: %v", err)
	}
}

// ---------- oggi ----------

type planItem struct {
	Piece      Piece
	Upcoming   []Concorso
	Base       float64
	Days       int
	Urgency    float64
	Need       float64
	Recency    float64
	Score      float64
	Conf       float64
	ConfRated  bool
	Since      int
	SinceNever bool
	Practiced  bool // a real session for this piece exists for today
	Minutes    int
	Logged     int
}

type todayData struct {
	Title           string
	Nav             string
	Date            string
	HasAvailability bool
	Free            []Availability
	Busy            []Availability
	Budget          int
	Logged          int // minutes recorded in today's diary
	LoggedPct       int // Logged as a percentage of Budget
	BudgetPct       int // LoggedPct capped at 100, for the bar width
	NoBudget        bool
	Items           []planItem
	OpenCount       int // plan items not yet practiced today
	DoneCount       int // pieces already practiced today, in and out of plan
	PlanTotal       int // plan items plus pieces practiced outside the plan
	ExtraDone       []doneEntry
	Pieces          []Piece // all active pieces, for the manual session form
	Readiness       []readinessRow
}

// doneEntry is a piece practiced today outside the plan, shown
// among the Completati.
type doneEntry struct {
	P      Piece
	Logged int
}

// readinessRow is one upcoming concorso with the mean preparation of
// its linked active pieces, as a percentage.
type readinessRow struct {
	ID     int64
	Name   string
	Date   string
	Days   int
	Pieces int
	Pct    int
}

// concorsoReadiness averages the preparation score of the active
// pieces linked to each upcoming concorso.
func (a *App) concorsoReadiness(today string, pieces []Piece) ([]readinessRow, error) {
	tiles, err := a.prepTiles(today, prepBiasDesktop)
	if err != nil {
		return nil, err
	}
	prep := make(map[int64]float64, len(tiles))
	for _, t := range tiles {
		prep[t.PieceID] = t.Prep
	}
	concorsi, err := a.listConcorsi(false)
	if err != nil {
		return nil, err
	}
	var out []readinessRow
	for _, c := range concorsi {
		if c.Date < today {
			continue
		}
		sum, n := 0.0, 0
		for _, p := range pieces {
			for _, pc := range p.Concorsi {
				if pc.ID == c.ID {
					sum += prep[p.ID]
					n++
				}
			}
		}
		if n == 0 {
			continue
		}
		days, err := daysBetween(today, c.Date)
		if err != nil {
			return nil, err
		}
		out = append(out, readinessRow{
			ID: c.ID, Name: c.Name, Date: c.Date, Days: days, Pieces: n,
			Pct: int(sum / float64(n) * 100),
		})
	}
	return out, nil
}

// upcomingConcorsi returns the piece's non-archived, not-yet-held concorsi
// with their weights, plus the days until the nearest one.
func upcomingConcorsi(p Piece, today string) ([]Concorso, []int, int, error) {
	var upcoming []Concorso
	var weights []int
	days := -1
	for _, c := range p.Concorsi {
		if c.Archived || c.Date < today {
			continue
		}
		upcoming = append(upcoming, c)
		weights = append(weights, c.Weight)
		d, err := daysBetween(today, c.Date)
		if err != nil {
			return nil, nil, 0, err
		}
		if days < 0 || d < days {
			days = d
		}
	}
	return upcoming, weights, days, nil
}

// scorePiece computes the daily plan item for one piece. ok=false means the
// piece is excluded from today's plan (no upcoming concorso or skipped).
func (a *App) scorePiece(p Piece, today string, coeffs Coeffs) (item planItem, ok bool, err error) {
	upcoming, weights, days, err := upcomingConcorsi(p, today)
	if err != nil {
		return planItem{}, false, err
	}
	if len(upcoming) == 0 {
		return planItem{}, false, nil
	}
	mark, err := a.todayMark(p.ID, today)
	if err != nil {
		return planItem{}, false, err
	}
	if mark.Skipped {
		return planItem{}, false, nil
	}
	conf, rated, err := a.latestConfidence(p.ID)
	if err != nil {
		return planItem{}, false, err
	}
	if !rated {
		conf = 2.5
	}
	since, practiced, err := a.daysSincePractice(p.ID, today)
	if err != nil {
		return planItem{}, false, err
	}
	sinceNever := !practiced
	if sinceNever {
		since = 30
	}
	br := ComputeScore(ScoreInput{
		Weights:    weights,
		Days:       days,
		Confidence: conf,
		SinceDays:  since,
	}, coeffs)
	return planItem{
		Piece:      p,
		Upcoming:   upcoming,
		Base:       br.Base,
		Days:       days,
		Urgency:    br.Urgency,
		Need:       br.Need,
		Recency:    br.Recency,
		Score:      br.Score,
		Conf:       conf,
		ConfRated:  rated,
		Since:      since,
		SinceNever: sinceNever,
		Practiced:  mark.Practiced,
		Logged:     mark.Logged,
	}, true, nil
}

func (a *App) buildPlan(today string) ([]planItem, int, error) {
	coeffs, err := a.getCoeffs()
	if err != nil {
		return nil, 0, err
	}
	pieces, err := a.listPieces(0, "", false)
	if err != nil {
		return nil, 0, err
	}
	var items []planItem
	for _, p := range pieces {
		item, ok, err := a.scorePiece(p, today, coeffs)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Base != items[j].Base {
			return items[i].Base > items[j].Base
		}
		return items[i].Piece.ID < items[j].Piece.ID
	})
	scores := make([]float64, len(items))
	for i := range items {
		scores[i] = items[i].Score
	}
	return items, len(scores), nil
}

// autoSkipOverflow drops the plan pieces that no longer fit today's
// remaining time, least urgent first: unpracticed pieces whose summed
// suggested minutes exceed what is left are marked 'saltato' for
// today, so they leave the plan with a trace in the diary.
func (a *App) autoSkipOverflow(items []planItem, remaining int, today string) ([]planItem, error) {
	sum := 0
	for _, it := range items {
		if !it.Practiced {
			sum += it.Minutes
		}
	}
	for sum > remaining {
		victim := -1
		for i := len(items) - 1; i >= 0; i-- {
			if !items[i].Practiced {
				victim = i
				break
			}
		}
		if victim < 0 {
			break
		}
		if _, err := a.db.Exec(`INSERT INTO sessions(date, piece_id, minutes, confidence, note)
			VALUES(?,?,0,0,'saltato')`, today, items[victim].Piece.ID); err != nil {
			return nil, err
		}
		log.Printf("event session auto-skipped piece=%d date=%s", items[victim].Piece.ID, today)
		sum -= items[victim].Minutes
		items = append(items[:victim], items[victim+1:]...)
	}
	return items, nil
}

func (a *App) handleToday(w http.ResponseWriter, _ *http.Request) {
	today := todayStr()
	avail, err := a.availabilityFor(today)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := todayData{Title: "Oggi", Nav: "oggi", Date: today, HasAvailability: len(avail) > 0}
	for _, v := range avail {
		if v.Kind == "busy" {
			data.Busy = append(data.Busy, v)
		} else {
			data.Free = append(data.Free, v)
			data.Budget += v.EndMin - v.StartMin
		}
	}
	var logged int
	if err := a.db.QueryRow(`SELECT COALESCE(SUM(minutes), 0) FROM sessions WHERE date=?`, today).Scan(&logged); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Logged = logged
	if data.Budget > 0 {
		data.LoggedPct = logged * 100 / data.Budget
		data.BudgetPct = data.LoggedPct
		if data.BudgetPct > 100 {
			data.BudgetPct = 100
		}
	}
	if data.HasAvailability && data.Budget > 0 {
		items, _, err := a.buildPlan(today)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		scores := make([]float64, len(items))
		for i := range items {
			scores[i] = items[i].Score
		}
		mins := AllocateMinutes(scores, data.Budget, 8)
		items = items[:len(mins)]
		for i := range items {
			items[i].Minutes = mins[i]
		}
		items, err = a.autoSkipOverflow(items, data.Budget-data.Logged, today)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data.Items = items
		for _, it := range items {
			if it.Practiced {
				data.DoneCount++
			} else {
				data.OpenCount++
			}
		}
	} else if data.HasAvailability {
		data.NoBudget = true
	}
	pieces, err := a.listPieces(0, "", false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Pieces = pieces
	// Preparation dots on plan pieces, extra pieces and concorso chips.
	{
		tmp := make([]Piece, 0, len(data.Items)+len(data.ExtraDone))
		for _, it := range data.Items {
			tmp = append(tmp, it.Piece)
		}
		for _, e := range data.ExtraDone {
			tmp = append(tmp, e.P)
		}
		if err := a.stampPrepLevels(tmp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		k := 0
		for i := range data.Items {
			stamped := tmp[k]
			k++
			data.Items[i].Piece = stamped
			for ui := range data.Items[i].Upcoming {
				for _, c := range stamped.Concorsi {
					if c.ID == data.Items[i].Upcoming[ui].ID {
						data.Items[i].Upcoming[ui].Level = c.Level
						data.Items[i].Upcoming[ui].Prep = c.Prep
					}
				}
			}
		}
		for i := range data.ExtraDone {
			data.ExtraDone[i].P = tmp[k]
			k++
		}
	}
	// Pieces practiced today outside the plan (recorded by hand) join
	// the hero count and the Completati list.
	inPlan := make(map[int64]bool, len(data.Items))
	for _, it := range data.Items {
		inPlan[it.Piece.ID] = true
	}
	byID := make(map[int64]Piece, len(pieces))
	for _, p := range pieces {
		byID[p.ID] = p
	}
	xrows, err := a.db.Query(`SELECT piece_id, COALESCE(SUM(minutes), 0)
		FROM sessions WHERE date = ? AND (minutes > 0 OR confidence > 0)
		GROUP BY piece_id ORDER BY piece_id`, today)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer closeRowsLogged(xrows)
	for xrows.Next() {
		var pid int64
		var mins int
		if err := xrows.Scan(&pid, &mins); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if inPlan[pid] {
			continue
		}
		p, ok := byID[pid]
		if !ok {
			continue
		}
		data.ExtraDone = append(data.ExtraDone, doneEntry{P: p, Logged: mins})
		data.DoneCount++
	}
	if err := xrows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.PlanTotal = len(data.Items) + len(data.ExtraDone)
	data.Readiness, err = a.concorsoReadiness(today, pieces)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "today.html", data)
}

func (a *App) handleAddAvailability(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	date := r.FormValue("date")
	if date == "" {
		date = todayStr()
	}
	start, ok1 := parseHHMM(r.FormValue("start"))
	end, ok2 := parseHHMM(r.FormValue("end"))
	if !ok1 || !ok2 || end <= start {
		http.Error(w, "orario non valido", http.StatusBadRequest)
		return
	}
	_, err := a.db.Exec(`INSERT INTO availability(date, start_min, end_min, label, kind)
		VALUES(?,?,?,?,'free')`, date, start, end, r.FormValue("label"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event availability added date=%s start=%d end=%d", date, start, end)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleDelAvailability(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := formInt(r, "id", 0)
	if id > 0 {
		if _, err := a.db.Exec(`DELETE FROM availability WHERE id=?`, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("event availability deleted id=%d", id)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pieceID := formInt(r, "piece_id", 0)
	minutes := formInt(r, "minutes", 0)
	conf := formInt(r, "confidence", 0)
	if pieceID <= 0 || minutes < 0 || conf < 1 || conf > 5 {
		http.Error(w, "dati seduta non validi", http.StatusBadRequest)
		return
	}
	var tempo any
	if raw := strings.TrimSpace(r.FormValue("tempo")); raw != "" {
		if bpm, err := strconv.Atoi(raw); err == nil && bpm > 0 && bpm <= 400 {
			tempo = bpm
		}
	}
	_, err := a.db.Exec(`INSERT INTO sessions(date, piece_id, minutes, confidence, note, tempo)
		VALUES(?,?,?,?,?,?)`, todayStr(), pieceID, minutes, conf, r.FormValue("note"), tempo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event session saved piece=%d date=%s minutes=%d confidence=%d", pieceID, todayStr(), minutes, conf)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) markSession(w http.ResponseWriter, r *http.Request, note string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pieceID := formInt(r, "piece_id", 0)
	if pieceID <= 0 {
		http.Error(w, "pezzo non valido", http.StatusBadRequest)
		return
	}
	_, err := a.db.Exec(`INSERT INTO sessions(date, piece_id, minutes, confidence, note)
		VALUES(?,?,0,0,?)`, todayStr(), pieceID, note)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event session marked piece=%d date=%s note=%s", pieceID, todayStr(), note)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleSkip(w http.ResponseWriter, r *http.Request) {
	a.markSession(w, r, "saltato")
}

// ---------- pezzi ----------

type pezziData struct {
	Title          string
	Nav            string
	Pieces         []Piece
	Concorsi       []Concorso
	FilterConcorso int64
	FilterKind     string
	ShowArchived   bool
	Query          string // raw query of the current list view, for return redirects
}

func (a *App) handlePezzi(w http.ResponseWriter, r *http.Request) {
	concorsoID := int64(formInt(r, "concorso", 0))
	kind := r.URL.Query().Get("kind")
	showArchived := r.URL.Query().Get("archiviati") == "1"
	pieces, err := a.listPieces(concorsoID, kind, showArchived)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.stampPrepLevels(pieces); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	concorsi, ok := a.concorsiOr500(w)
	if !ok {
		return
	}
	a.render(w, "pezzi.html", pezziData{
		Title: "Pezzi", Nav: "pezzi", Pieces: pieces, Concorsi: concorsi,
		FilterConcorso: concorsoID, FilterKind: kind, ShowArchived: showArchived,
		Query: r.URL.RawQuery,
	})
}

func clampDifficulty(r *http.Request) int {
	d, err := strconv.Atoi(r.FormValue("difficulty"))
	if err != nil || d < 1 {
		return 1
	}
	if d > 5 {
		return 5
	}
	return d
}

// linkEstratto reads the per-link excerpt submitted for a concorso checkbox.
func linkEstratto(r *http.Request, concorsoID int64) string {
	return strings.TrimSpace(r.FormValue("estratto-" + strconv.FormatInt(concorsoID, 10)))
}

func (a *App) handleAddPiece(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kind := r.FormValue("kind")
	if kind != kindSolo {
		kind = kindPasso
	}
	composer := strings.TrimSpace(r.FormValue("composer"))
	work := strings.TrimSpace(r.FormValue("work"))
	if composer == "" || work == "" {
		http.Error(w, "compositore e opera sono obbligatori", http.StatusBadRequest)
		return
	}
	res, err := a.db.Exec(`INSERT INTO pieces(composer, work, movement, kind, difficulty)
		VALUES(?,?,?,?,?)`, composer, work, r.FormValue("movement"), kind, clampDifficulty(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pid, err := res.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, cid := range formIDs(r, "concorso") {
		if _, err := a.db.Exec(`INSERT OR IGNORE INTO piece_concorso(piece_id, concorso_id, estratto) VALUES(?,?,?)`, pid, cid, linkEstratto(r, cid)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	log.Printf("event piece created id=%d kind=%s", pid, kind)
	http.Redirect(w, r, "/pezzi", http.StatusSeeOther)
}

type pieceFormData struct {
	Title    string
	Nav      string
	Piece    Piece
	Concorsi []Concorso
	Selected map[int64]bool
	Estratto map[int64]string
}

func (a *App) handleEditPiece(w http.ResponseWriter, r *http.Request) {
	p, ok := a.pieceOr404(w, r)
	if !ok {
		return
	}
	concorsi, ok := a.concorsiOr500(w)
	if !ok {
		return
	}
	sel := map[int64]bool{}
	estratti := map[int64]string{}
	for _, c := range p.Concorsi {
		sel[c.ID] = true
		estratti[c.ID] = c.Estratto
	}
	a.render(w, "pezzo_form.html", pieceFormData{
		Title: "Modifica pezzo", Nav: "pezzi", Piece: p, Concorsi: concorsi, Selected: sel, Estratto: estratti,
	})
}

func (a *App) handleSetDifficulty(w http.ResponseWriter, r *http.Request) {
	id, ok := formID(w, r)
	if !ok {
		return
	}
	d := clampDifficulty(r)
	res, err := a.db.Exec(`UPDATE pieces SET difficulty=? WHERE id=?`, d, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.NotFound(w, r)
		return
	}
	log.Printf("event piece difficulty id=%d value=%d", id, d)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleSetPriority(w http.ResponseWriter, r *http.Request) {
	id, ok := formID(w, r)
	if !ok {
		return
	}
	p, err := strconv.Atoi(r.FormValue("priority"))
	if err != nil {
		p = 1
	}
	res, err := a.db.Exec(`UPDATE concorsi SET weight=? WHERE id=?`, validWeight(p), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.NotFound(w, r)
		return
	}
	log.Printf("event concorso priority id=%d value=%d", id, validWeight(p))
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleUpdatePiece(w http.ResponseWriter, r *http.Request) {
	id, ok := formID(w, r)
	if !ok {
		return
	}
	kind := r.FormValue("kind")
	if kind != kindSolo {
		kind = kindPasso
	}
	_, err := a.db.Exec(`UPDATE pieces SET composer=?, work=?, movement=?, kind=?, difficulty=?
		WHERE id=?`, r.FormValue("composer"), r.FormValue("work"),
		r.FormValue("movement"), kind, clampDifficulty(r), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			log.Printf("rollback: %v", rerr)
		}
	}()
	if _, err := tx.Exec(`DELETE FROM piece_concorso WHERE piece_id=?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, cid := range formIDs(r, "concorso") {
		if _, err := tx.Exec(`INSERT INTO piece_concorso(piece_id, concorso_id, estratto) VALUES(?,?,?)`, id, cid, linkEstratto(r, cid)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event piece updated id=%d", id)
	http.Redirect(w, r, "/pezzi", http.StatusSeeOther)
}

// setArchived runs an archive/restore UPDATE for one row and redirects.
// The query is a static string chosen by the caller; what labels the event
// in the log. If the form carries a "ritorna" value pointing at /pezzi or
// at the piece detail page, it is used as the redirect so the user lands
// back where they were.
func (a *App) setArchived(w http.ResponseWriter, r *http.Request, query, redirect, what string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if back := r.FormValue("ritorna"); strings.HasPrefix(back, "/pezzi") || strings.HasPrefix(back, "/diario/pezzo/") || strings.HasPrefix(back, "/concorsi/") {
		redirect = back
	}
	if _, err := a.db.Exec(query, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event %s id=%d", what, id)
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (a *App) handleArchivePiece(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE pieces SET archived_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, "/pezzi", "piece archived")
}

func (a *App) handleRestorePiece(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE pieces SET archived_at=NULL WHERE id=?`, "/pezzi?archiviati=1", "piece restored")
}

// ---------- concorsi ----------

type concorsoRow struct {
	Concorso
	Concluded bool
	Pieces    int
	Links     int
}

type concorsiData struct {
	Title string
	Nav   string
	Rows  []concorsoRow
	Today string
}

func (a *App) handleConcorsi(w http.ResponseWriter, _ *http.Request) {
	today := todayStr()
	cs, err := a.listConcorsi(true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var rows []concorsoRow
	for _, c := range cs {
		var n, nl int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM piece_concorso WHERE concorso_id=?`, c.ID).Scan(&n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM concorso_links WHERE concorso_id=?`, c.ID).Scan(&nl); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows = append(rows, concorsoRow{
			Concorso:  c,
			Concluded: !c.Archived && c.Date < today,
			Pieces:    n,
			Links:     nl,
		})
	}
	levels, means, err := a.concorsoPrepStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for i := range rows {
		rows[i].Level = levels[rows[i].ID]
		rows[i].Prep = means[rows[i].ID]
	}
	a.render(w, "concorsi.html", concorsiData{Title: "Concorsi", Nav: "concorsi", Rows: rows, Today: today})
}

func validWeight(n int) int {
	if n < 1 {
		return 1
	}
	if n > 5 {
		return 5
	}
	return n
}

func (a *App) handleAddConcorso(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name, date, ok := concorsoNameDate(w, r)
	if !ok {
		return
	}
	links, lok := formResourceLinks(w, r)
	if !lok {
		return
	}
	res, err := a.db.Exec(`INSERT INTO concorsi(name, audition_date, weight)
		VALUES(?,?,?)`, name, date, validWeight(formInt(r, "weight", 1)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if id, ierr := res.LastInsertId(); ierr == nil {
		log.Printf("event concorso created id=%d date=%s", id, date)
		for _, l := range links {
			if err := a.addConcorsoLink(id, l.Label, l.URL); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	http.Redirect(w, r, "/concorsi", http.StatusSeeOther)
}

func (a *App) handleUpdateConcorso(w http.ResponseWriter, r *http.Request) {
	id, ok := formID(w, r)
	if !ok {
		return
	}
	name, date, ok := concorsoNameDate(w, r)
	if !ok {
		return
	}
	_, err := a.db.Exec(`UPDATE concorsi SET name=?, audition_date=?, weight=?
		WHERE id=?`, name, date, validWeight(formInt(r, "weight", 1)), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event concorso updated id=%d date=%s", id, date)
	redirect := "/concorsi"
	if back := r.FormValue("ritorna"); strings.HasPrefix(back, "/concorsi/") {
		redirect = back
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (a *App) handleArchiveConcorso(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE concorsi SET archived_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, "/concorsi", "concorso archived")
}

func (a *App) handleRestoreConcorso(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE concorsi SET archived_at=NULL WHERE id=?`, "/concorsi", "concorso restored")
}

func (a *App) concorsoOr404(w http.ResponseWriter, r *http.Request) (Concorso, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return Concorso{}, false
	}
	var c Concorso
	err := a.db.QueryRow(`SELECT id, name, audition_date, weight, archived_at IS NOT NULL
		FROM concorsi WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Date, &c.Weight, &c.Archived)
	if err != nil {
		http.NotFound(w, r)
		return Concorso{}, false
	}
	return c, true
}

// validResourceURL accepts only absolute http(s) URLs with a host.
func validResourceURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// resourceLabel defaults an empty link label to the URL host.
func resourceLabel(raw, label string) string {
	if l := strings.TrimSpace(label); l != "" {
		return l
	}
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Host != "" {
		return u.Host
	}
	return "Link"
}

// formResourceLinks collects the (label, url) pairs submitted by a form:
// empty URLs are skipped, invalid ones abort with a 400 already written.
func formResourceLinks(w http.ResponseWriter, r *http.Request) ([]ConcorsoLink, bool) {
	urls := r.Form["link_url"]
	labels := r.Form["link_label"]
	var out []ConcorsoLink
	for i, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		if !validResourceURL(u) {
			http.Error(w, "link risorsa non valido: serve un URL http(s) completo", http.StatusBadRequest)
			return nil, false
		}
		label := ""
		if i < len(labels) {
			label = labels[i]
		}
		out = append(out, ConcorsoLink{Label: resourceLabel(u, label), URL: u})
	}
	return out, true
}

type concorsoDetailData struct {
	Title         string
	Nav           string
	Concorso      Concorso
	Concluded     bool
	Links         []ConcorsoLink
	Pieces        []Piece
	PrepMap       []prepTile
	PrepMapMobile []prepTile
}

func (a *App) handleConcorsoDetail(w http.ResponseWriter, r *http.Request) {
	c, ok := a.concorsoOr404(w, r)
	if !ok {
		return
	}
	links, err := a.concorsoLinks(c.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pieces, err := a.listPieces(c.ID, "", false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.stampPrepLevels(pieces); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	{
		levels, means, err := a.concorsoPrepStats()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		c.Level = levels[c.ID]
		c.Prep = means[c.ID]
	}
	filter := map[int64]bool{c.ID: true}
	today := todayStr()
	prep, err := a.prepTilesFor(today, prepBiasDesktop, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	prepMobile, err := a.prepTilesFor(today, prepBiasMobile, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "concorso_detail.html", concorsoDetailData{
		Title: c.Name, Nav: "concorsi", Concorso: c,
		Concluded: !c.Archived && c.Date < todayStr(),
		Links:     links, Pieces: pieces,
		PrepMap: prep, PrepMapMobile: prepMobile,
	})
}

type concorsoFormData struct {
	Title    string
	Nav      string
	Concorso Concorso
	Links    []ConcorsoLink
}

// handleConcorsoEdit renders the concorso edit panel (fields + resource
// links), mirroring the piece edit page.
func (a *App) handleConcorsoEdit(w http.ResponseWriter, r *http.Request) {
	c, ok := a.concorsoOr404(w, r)
	if !ok {
		return
	}
	links, err := a.concorsoLinks(c.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "concorso_form.html", concorsoFormData{
		Title: "Modifica concorso", Nav: "concorsi", Concorso: c, Links: links,
	})
}

func (a *App) handleAddConcorsoLink(w http.ResponseWriter, r *http.Request) {
	c, ok := a.concorsoOr404(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	links, lok := formResourceLinks(w, r)
	if !lok {
		return
	}
	if len(links) == 0 {
		http.Error(w, "link risorsa mancante", http.StatusBadRequest)
		return
	}
	for _, l := range links {
		if err := a.addConcorsoLink(c.ID, l.Label, l.URL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	log.Printf("event concorso link added concorso=%d", c.ID)
	http.Redirect(w, r, "/concorsi/"+strconv.FormatInt(c.ID, 10)+"/modifica", http.StatusSeeOther)
}

func (a *App) handleDeleteConcorsoLink(w http.ResponseWriter, r *http.Request) {
	c, ok := a.concorsoOr404(w, r)
	if !ok {
		return
	}
	linkID, err := strconv.ParseInt(r.PathValue("linkID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.deleteConcorsoLink(c.ID, linkID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("event concorso link removed concorso=%d link=%d", c.ID, linkID)
	http.Redirect(w, r, "/concorsi/"+strconv.FormatInt(c.ID, 10)+"/modifica", http.StatusSeeOther)
}

// ---------- diario ----------

type dayRow struct {
	Date    string
	Minutes int
	Pieces  int
}

// concorsoChip is one toggle chip above the preparation map.
type concorsoChip struct {
	ID       int64
	Name     string
	Archived bool
	Selected bool
	Href     string
	Level    int     // mean preparation level 0..4 of the concorso
	Prep     float64 // mean preparation score 0..1 of the concorso
}

// parseConcorsoFilter reads the ?c=1,2 map-filter selection.
func parseConcorsoFilter(raw string) map[int64]bool {
	sel := map[int64]bool{}
	for _, part := range strings.Split(raw, ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && id > 0 {
			sel[id] = true
		}
	}
	return sel
}

// concorsoFilterHref renders a selection as its /diario URL; an empty
// selection is the plain Tutti view.
func concorsoFilterHref(sel map[int64]bool) string {
	if len(sel) == 0 {
		return "/diario"
	}
	ids := make([]int64, 0, len(sel))
	for id := range sel {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "/diario?c=" + strings.Join(parts, ",")
}

func (a *App) handleDiario(w http.ResponseWriter, r *http.Request) {
	rows, ok := a.queryRows(w, `SELECT date, COALESCE(SUM(minutes),0),
		COUNT(DISTINCT CASE WHEN minutes > 0 OR confidence > 0 THEN piece_id END)
		FROM sessions GROUP BY date ORDER BY date DESC`)
	if !ok {
		return
	}
	defer closeRowsLogged(rows)
	var days []dayRow
	for rows.Next() {
		var d dayRow
		if err := rows.Scan(&d.Date, &d.Minutes, &d.Pieces); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		days = append(days, d)
	}
	concorsi, err := a.listConcorsi(true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sel := parseConcorsoFilter(r.URL.Query().Get("c"))
	valid := make(map[int64]bool, len(sel))
	for _, c := range concorsi {
		if sel[c.ID] {
			valid[c.ID] = true
		}
	}
	chips := make([]concorsoChip, 0, len(concorsi))
	chipLevels, chipMeans, err := a.concorsoPrepStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, c := range concorsi {
		next := make(map[int64]bool, len(valid)+1)
		for id := range valid {
			next[id] = true
		}
		if next[c.ID] {
			delete(next, c.ID)
		} else {
			next[c.ID] = true
		}
		chips = append(chips, concorsoChip{ID: c.ID, Name: c.Name, Archived: c.Archived, Selected: valid[c.ID], Href: concorsoFilterHref(next), Level: chipLevels[c.ID], Prep: chipMeans[c.ID]})
	}
	var filter map[int64]bool
	if len(valid) > 0 {
		filter = valid
	}
	prep, err := a.prepTilesFor(todayStr(), prepBiasDesktop, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	prepMobile, err := a.prepTilesFor(todayStr(), prepBiasMobile, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "diario.html", map[string]any{"Title": "Diario", "Nav": "diario", "Days": days, "PrepMap": prep, "PrepMapMobile": prepMobile, "Chips": chips, "TuttiOn": len(valid) == 0, "Filtered": len(valid) > 0})
}

type giornoPiece struct {
	PieceID  int64
	Title    string
	Movement string
	Minutes  int
	Level    int     // preparation level 0..4 of the piece
	Prep     float64 // preparation score 0..1 of the piece
	Sessions []Session
}

type giornoData struct {
	Title     string
	Nav       string
	Date      string
	Minutes   int
	Practiced int // pieces with a real session that day (skips excluded)
	Entries   []giornoPiece
}

// handleDiarioGiorno shows one day with every piece that concerns it.
func (a *App) handleDiarioGiorno(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		http.NotFound(w, r)
		return
	}
	tiles, err := a.prepTiles(todayStr(), prepBiasDesktop)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pieceLevel := make(map[int64]int, len(tiles))
	piecePrep := make(map[int64]float64, len(tiles))
	for _, t := range tiles {
		pieceLevel[t.PieceID] = t.Level
		piecePrep[t.PieceID] = t.Prep
	}
	rows, ok := a.queryRows(w, `SELECT s.id, s.date, s.piece_id, s.minutes, s.confidence, s.note, s.tempo,
		p.id, p.composer, p.work, p.movement
		FROM sessions s JOIN pieces p ON p.id = s.piece_id
		WHERE s.date = ? ORDER BY p.composer, p.work, s.id`, date)
	if !ok {
		return
	}
	defer closeRowsLogged(rows)
	data := giornoData{Title: "Diario", Nav: "diario", Date: date}
	byKey := map[int64]int{}
	counted := map[int64]bool{}
	for rows.Next() {
		var s Session
		var pid int64
		var composer, work, movement string
		if err := rows.Scan(&s.ID, &s.Date, &s.PieceID, &s.Minutes, &s.Confidence, &s.Note, &s.Tempo,
			&pid, &composer, &work, &movement); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		idx, seen := byKey[s.PieceID]
		if !seen {
			title := composer + " — " + work
			data.Entries = append(data.Entries, giornoPiece{PieceID: s.PieceID, Title: title, Movement: movement, Level: pieceLevel[s.PieceID], Prep: piecePrep[s.PieceID]})
			idx = len(data.Entries) - 1
			byKey[s.PieceID] = idx
		}
		if (s.Minutes > 0 || s.Confidence > 0) && !counted[s.PieceID] {
			counted[s.PieceID] = true
			data.Practiced++
		}
		data.Entries[idx].Sessions = append(data.Entries[idx].Sessions, s)
		data.Entries[idx].Minutes += s.Minutes
		data.Minutes += s.Minutes
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "giornata.html", data)
}

type pezzoDetailData struct {
	Title    string
	Nav      string
	Piece    Piece
	Sessions []Session
	Spark    sparkline
}

type sparkPoint struct {
	X, Y string
}

// sparkline holds precomputed SVG coordinates; the template renders them
// through html/template so nothing is ever injected as raw HTML.
type sparkline struct {
	Points []sparkPoint
}

func buildSparkline(confs []int) sparkline {
	const W, H = 260, 64
	var s sparkline
	for i, c := range confs {
		var x float64
		if len(confs) == 1 {
			x = float64(W) / 2
		} else {
			x = float64(i) * float64(W) / float64(len(confs)-1)
		}
		y := float64(H) - 5 - float64(c-1)/4*(float64(H)-10)
		s.Points = append(s.Points, sparkPoint{
			X: strconv.FormatFloat(x, 'f', 1, 64),
			Y: strconv.FormatFloat(y, 'f', 1, 64),
		})
	}
	return s
}

func (a *App) handlePezzoDetail(w http.ResponseWriter, r *http.Request) {
	p, ok := a.pieceOr404(w, r)
	if !ok {
		return
	}
	rows, ok := a.queryRows(w, `SELECT id, date, piece_id, minutes, confidence, note, tempo FROM sessions
		WHERE piece_id=? ORDER BY date DESC, id DESC`, p.ID)
	if !ok {
		return
	}
	defer closeRowsLogged(rows)
	var sessions []Session
	var confs []int // oldest -> newest for the sparkline
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Date, &s.PieceID, &s.Minutes, &s.Confidence, &s.Note, &s.Tempo); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sessions = append(sessions, s)
		if s.Confidence > 0 {
			confs = append([]int{s.Confidence}, confs...)
		}
	}
	stamped := []Piece{p}
	if err := a.stampPrepLevels(stamped); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	p = stamped[0]
	a.render(w, "pezzo_detail.html", pezzoDetailData{
		Title: p.Composer + " — " + p.Work, Nav: "pezzi", Piece: p, Sessions: sessions, Spark: buildSparkline(confs),
	})
}

// ---------- impostazioni ----------

type impostazioniData struct {
	Title    string
	Nav      string
	Coeffs   Coeffs
	Concorsi []Concorso
}

func (a *App) handleImpostazioni(w http.ResponseWriter, _ *http.Request) {
	coeffs, err := a.getCoeffs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	concorsi, ok := a.concorsiOr500(w)
	if !ok {
		return
	}
	a.render(w, "impostazioni.html", impostazioniData{
		Title: "Impostazioni", Nav: "impostazioni", Coeffs: coeffs, Concorsi: concorsi,
	})
}

func (a *App) handleSaveImpostazioni(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, kv := range []struct {
		key string
		val string
	}{
		{"urgency_k", r.FormValue("urgency_k")},
		{"urgency_horizon", r.FormValue("urgency_horizon")},
		{"recency_cap", r.FormValue("recency_cap")},
	} {
		f, err := strconv.ParseFloat(kv.val, 64)
		if err != nil || f <= 0 {
			http.Error(w, "coefficiente non valido: "+kv.key, http.StatusBadRequest)
			return
		}
		if err := a.setCoeff(kv.key, f); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	log.Printf("event settings saved")
	http.Redirect(w, r, "/impostazioni", http.StatusSeeOther)
}
