package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// pct renders a 0..1 score as a rounded percentage number.
func pct(score float64) int {
	return int(math.Round(score * 100))
}

//go:embed templates/*.html
var tmplFS embed.FS

//go:embed static
var staticFS embed.FS

var weekdaysIT = []string{
	"domenica", "lunedì", "martedì", "mercoledì", "giovedì", "venerdì", "sabato",
}

var monthsIT = []string{
	"", "gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno",
	"luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre",
}

func dateIT(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return fmt.Sprintf("%s %d %s %d",
		weekdaysIT[t.Weekday()], t.Day(), monthsIT[int(t.Month())], t.Year())
}

// dateDMY renders an ISO date as dd/mm/yyyy, for compact axis labels.
func dateDMY(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("02/01/2006")
}

func hhmm(mins int) string {
	if mins < 0 {
		return "—"
	}
	return fmt.Sprintf("%02d:%02d", mins/60, mins%60)
}

func f1(f float64) string { return fmt.Sprintf("%.1f", f) }

func confLabel(conf float64, rated bool) string {
	if !rated {
		return "mai valutata"
	}
	if conf < 1 {
		return "mai toccato"
	}
	return fmt.Sprintf("%.0f/5", conf)
}

func sinceLabel(since int, never bool) string {
	if never {
		return "mai studiato"
	}
	if since == 0 {
		return "studiato oggi"
	}
	if since == 1 {
		return "ieri"
	}
	return fmt.Sprintf("%d giorni fa", since)
}

// urlSplit holds the two renderable halves of a resource URL.
type urlSplit struct{ Head, Tail string }

// urlParts splits a URL into its scheme://host head and the remaining tail,
// so templates can render it on one line keeping the end visible.
// Unparseable or host-less URLs come back whole as the head.
func urlParts(raw string) urlSplit {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return urlSplit{Head: raw}
	}
	head := u.Scheme + "://" + u.Host
	tail := strings.TrimPrefix(raw, head)
	return urlSplit{Head: head, Tail: strings.TrimPrefix(tail, "/")}
}

func tmplFuncs() template.FuncMap {
	return template.FuncMap{
		"f1":         f1,
		"pct":        pct,
		"urlParts":   urlParts,
		"hhmm":       hhmm,
		"dateIT":     dateIT,
		"dateDMY":    dateDMY,
		"confLabel":  confLabel,
		"sinceLabel": sinceLabel,
		"join":       strings.Join,
	}
}

// render parses layout.html together with one page template (which must define
// "content") and executes "layout".
func (a *App) render(w http.ResponseWriter, page string, data any) {
	t, err := template.New("layout").Funcs(tmplFuncs()).ParseFS(
		tmplFS, "templates/layout.html", "templates/partials.html", "templates/"+page)
	if err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
	}
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
