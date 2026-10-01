package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
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
	mux.HandleFunc("POST /sessione/rimanda", a.handlePostpone)
	mux.HandleFunc("POST /sessione/salta", a.handleSkip)
	mux.HandleFunc("GET /pezzi", a.handlePezzi)
	mux.HandleFunc("POST /pezzi", a.handleAddPiece)
	mux.HandleFunc("GET /pezzi/{id}/modifica", a.handleEditPiece)
	mux.HandleFunc("POST /pezzi/{id}/modifica", a.handleUpdatePiece)
	mux.HandleFunc("POST /pezzi/{id}/archivia", a.handleArchivePiece)
	mux.HandleFunc("POST /pezzi/{id}/ripristina", a.handleRestorePiece)
	mux.HandleFunc("GET /concorsi", a.handleConcorsi)
	mux.HandleFunc("POST /concorsi", a.handleAddConcorso)
	mux.HandleFunc("POST /concorsi/{id}", a.handleUpdateConcorso)
	mux.HandleFunc("POST /concorsi/{id}/archivia", a.handleArchiveConcorso)
	mux.HandleFunc("POST /concorsi/{id}/ripristina", a.handleRestoreConcorso)
	mux.HandleFunc("GET /diario", a.handleDiario)
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
	Pinned     bool
	Minutes    int
	StartMin   int // -1 when it does not fit any free window
	EndMin     int
	Logged     int
}

type todayData struct {
	Title           string
	Date            string
	HasAvailability bool
	Free            []Availability
	Busy            []Availability
	Budget          int
	NoBudget        bool
	Items           []planItem
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
func (a *App) scorePiece(p Piece, today, yday string, coeffs Coeffs) (item planItem, ok bool, err error) {
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
	pinned, err := a.postponedYesterday(p.ID, yday)
	if err != nil {
		return planItem{}, false, err
	}
	br := ComputeScore(ScoreInput{
		Weights:    weights,
		Days:       days,
		Confidence: conf,
		SinceDays:  since,
		Pinned:     pinned,
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
		Pinned:     pinned,
		Logged:     mark.Logged,
		StartMin:   -1,
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
	yday := ""
	if t, err := time.Parse("2006-01-02", today); err == nil {
		yday = t.AddDate(0, 0, -1).Format("2006-01-02")
	}
	var items []planItem
	for _, p := range pieces {
		item, ok, err := a.scorePiece(p, today, yday, coeffs)
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

func (a *App) handleToday(w http.ResponseWriter, _ *http.Request) {
	today := todayStr()
	avail, err := a.availabilityFor(today)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := todayData{Title: "Oggi", Date: today, HasAvailability: len(avail) > 0}
	for _, v := range avail {
		if v.Kind == "busy" {
			data.Busy = append(data.Busy, v)
		} else {
			data.Free = append(data.Free, v)
			data.Budget += v.EndMin - v.StartMin
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
		// Lay the pieces out sequentially across the free windows.
		wi := 0
		cursor := -1
		for i := range items {
			for wi < len(data.Free) {
				wstart := data.Free[wi].StartMin
				if cursor < wstart {
					cursor = wstart
				}
				if cursor+items[i].Minutes <= data.Free[wi].EndMin {
					break
				}
				wi++
				cursor = -1
			}
			if wi >= len(data.Free) {
				break // does not fit: StartMin stays -1 ("fuori fascia")
			}
			items[i].StartMin = cursor
			cursor += items[i].Minutes
			items[i].EndMin = cursor
		}
		data.Items = items
	} else if data.HasAvailability {
		data.NoBudget = true
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
	kind := r.FormValue("kind")
	if kind != "busy" {
		kind = "free"
	}
	if !ok1 || !ok2 || end <= start {
		http.Error(w, "orario non valido", http.StatusBadRequest)
		return
	}
	_, err := a.db.Exec(`INSERT INTO availability(date, start_min, end_min, label, kind)
		VALUES(?,?,?,?,?)`, date, start, end, r.FormValue("label"), kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	_, err := a.db.Exec(`INSERT INTO sessions(date, piece_id, minutes, confidence, note)
		VALUES(?,?,?,?,?)`, todayStr(), pieceID, minutes, conf, r.FormValue("note"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handlePostpone(w http.ResponseWriter, r *http.Request) {
	a.markSession(w, r, "rimandato")
}

func (a *App) handleSkip(w http.ResponseWriter, r *http.Request) {
	a.markSession(w, r, "saltato")
}

// ---------- pezzi ----------

type pezziData struct {
	Title          string
	Pieces         []Piece
	Concorsi       []Concorso
	FilterConcorso int64
	FilterKind     string
	ShowArchived   bool
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
	concorsi, err := a.listConcorsi(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "pezzi.html", pezziData{
		Title: "Pezzi", Pieces: pieces, Concorsi: concorsi,
		FilterConcorso: concorsoID, FilterKind: kind, ShowArchived: showArchived,
	})
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
	res, err := a.db.Exec(`INSERT INTO pieces(composer, work, movement, excerpt, kind)
		VALUES(?,?,?,?,?)`, composer, work, r.FormValue("movement"), r.FormValue("excerpt"), kind)
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
		if _, err := a.db.Exec(`INSERT OR IGNORE INTO piece_concorso(piece_id, concorso_id) VALUES(?,?)`, pid, cid); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/pezzi", http.StatusSeeOther)
}

type pieceFormData struct {
	Title    string
	Piece    Piece
	Concorsi []Concorso
	Selected map[int64]bool
}

func (a *App) handleEditPiece(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := a.getPiece(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	concorsi, err := a.listConcorsi(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sel := map[int64]bool{}
	for _, c := range p.Concorsi {
		sel[c.ID] = true
	}
	a.render(w, "pezzo_form.html", pieceFormData{
		Title: "Modifica pezzo", Piece: p, Concorsi: concorsi, Selected: sel,
	})
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
	_, err := a.db.Exec(`UPDATE pieces SET composer=?, work=?, movement=?, excerpt=?, kind=?
		WHERE id=?`, r.FormValue("composer"), r.FormValue("work"),
		r.FormValue("movement"), r.FormValue("excerpt"), kind, id)
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
		if _, err := tx.Exec(`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(?,?)`, id, cid); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/pezzi", http.StatusSeeOther)
}

// setArchived runs an archive/restore UPDATE for one row and redirects.
// The query is a static string chosen by the caller.
func (a *App) setArchived(w http.ResponseWriter, r *http.Request, query, redirect string) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(query, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (a *App) handleArchivePiece(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE pieces SET archived_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, "/pezzi")
}

func (a *App) handleRestorePiece(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE pieces SET archived_at=NULL WHERE id=?`, "/pezzi?archiviati=1")
}

// ---------- concorsi ----------

type concorsoRow struct {
	Concorso
	Concluded bool
	Pieces    int
}

type concorsiData struct {
	Title string
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
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM piece_concorso WHERE concorso_id=?`, c.ID).Scan(&n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows = append(rows, concorsoRow{
			Concorso:  c,
			Concluded: !c.Archived && c.Date < today,
			Pieces:    n,
		})
	}
	a.render(w, "concorsi.html", concorsiData{Title: "Concorsi", Rows: rows, Today: today})
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
	name := strings.TrimSpace(r.FormValue("name"))
	date := r.FormValue("date")
	if name == "" || date == "" {
		http.Error(w, "nome e data sono obbligatori", http.StatusBadRequest)
		return
	}
	_, err := a.db.Exec(`INSERT INTO concorsi(name, city, audition_date, weight)
		VALUES(?,?,?,?)`, name, r.FormValue("city"), date, validWeight(formInt(r, "weight", 1)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/concorsi", http.StatusSeeOther)
}

func (a *App) handleUpdateConcorso(w http.ResponseWriter, r *http.Request) {
	id, ok := formID(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	date := r.FormValue("date")
	if name == "" || date == "" {
		http.Error(w, "nome e data sono obbligatori", http.StatusBadRequest)
		return
	}
	_, err := a.db.Exec(`UPDATE concorsi SET name=?, city=?, audition_date=?, weight=?
		WHERE id=?`, name, r.FormValue("city"), date, validWeight(formInt(r, "weight", 1)), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/concorsi", http.StatusSeeOther)
}

func (a *App) handleArchiveConcorso(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE concorsi SET archived_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, "/concorsi")
}

func (a *App) handleRestoreConcorso(w http.ResponseWriter, r *http.Request) {
	a.setArchived(w, r, `UPDATE concorsi SET archived_at=NULL WHERE id=?`, "/concorsi")
}

// ---------- diario ----------

type dayRow struct {
	Date    string
	Minutes int
	Pieces  int
}

func (a *App) handleDiario(w http.ResponseWriter, _ *http.Request) {
	rows, err := a.db.Query(`SELECT date, COALESCE(SUM(minutes),0), COUNT(DISTINCT piece_id)
		FROM sessions GROUP BY date ORDER BY date DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("rows close: %v", err)
		}
	}()
	var days []dayRow
	for rows.Next() {
		var d dayRow
		if err := rows.Scan(&d.Date, &d.Minutes, &d.Pieces); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		days = append(days, d)
	}
	a.render(w, "diario.html", map[string]any{"Title": "Diario", "Days": days})
}

type pezzoDetailData struct {
	Title    string
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
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := a.getPiece(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rows, err := a.db.Query(`SELECT id, date, piece_id, minutes, confidence, note FROM sessions
		WHERE piece_id=? ORDER BY date DESC, id DESC`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("rows close: %v", err)
		}
	}()
	var sessions []Session
	var confs []int // oldest -> newest for the sparkline
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Date, &s.PieceID, &s.Minutes, &s.Confidence, &s.Note); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sessions = append(sessions, s)
		if s.Confidence > 0 {
			confs = append([]int{s.Confidence}, confs...)
		}
	}
	a.render(w, "pezzo_detail.html", pezzoDetailData{
		Title: p.Composer + " — " + p.Work, Piece: p, Sessions: sessions, Spark: buildSparkline(confs),
	})
}

// ---------- impostazioni ----------

type impostazioniData struct {
	Title    string
	Coeffs   Coeffs
	Concorsi []Concorso
}

func (a *App) handleImpostazioni(w http.ResponseWriter, _ *http.Request) {
	coeffs, err := a.getCoeffs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	concorsi, err := a.listConcorsi(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "impostazioni.html", impostazioniData{
		Title: "Impostazioni", Coeffs: coeffs, Concorsi: concorsi,
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
	http.Redirect(w, r, "/impostazioni", http.StatusSeeOther)
}
