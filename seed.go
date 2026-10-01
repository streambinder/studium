package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
)

type seedConcorso struct {
	name, city, date string
	weight           int
}

type seedPiece struct {
	composer, work, movement, excerpt, kind string
	concorsi                                []int // 1-based indices into seedConcorsi
}

var seedConcorsi = []seedConcorso{
	{"Santa Cecilia — Violoncello di fila", "Roma", "2027-01-08", 4},
	{"Opera di Roma — Secondo Violoncello", "Roma", "2026-11-23", 3},
	{"San Carlo Napoli — Violoncello di fila", "Napoli", "2026-12-14", 2},
	{"Cagliari — Secondo / Fila", "Cagliari", "2026-11-16", 1},
}

// Index legend: 1 = Santa Cecilia, 2 = Opera di Roma, 3 = San Carlo, 4 = Cagliari.
var seedPieces = []seedPiece{
	// ---- passi d'orchestra ----
	{
		"Beethoven", "Sinfonia n. 5", "II mov. Andante con moto",
		"Roma: inizio–b.123, b.180–195 · San Carlo: b.1–123 · Cagliari: Andante con moto", "passo",
		[]int{1, 2, 3, 4},
	},
	{
		"Beethoven", "Sinfonia n. 5", "III mov.",
		"Roma: inizio–b.218 · Santa Cecilia: II–III mov.", "passo",
		[]int{1, 2},
	},
	{
		"Beethoven", "Sinfonia n. 7", "II mov. Allegretto",
		"Roma: inizio–b.98 · San Carlo: da b.27 (lett. A) · Cagliari: Vivace e Allegretto", "passo",
		[]int{2, 3, 4},
	},
	{
		"Beethoven", "Sinfonia n. 9", "Recitativo",
		"Cagliari", "passo",
		[]int{4},
	},
	{
		"Beethoven", "Sinfonia n. 5 (?)", "Allegro (movimento da verificare)",
		"b.132–209, lett. B, armatura 3 bemolli — pagina senza intestazione nel PDF passi San Carlo (p.8)", "passo",
		[]int{3},
	},
	{
		"Brahms", "Sinfonia n. 2", "II mov. Adagio non troppo",
		"Roma: inizio–b.32, b.43–67 · San Carlo: dall'inizio · Cagliari: Adagio non troppo", "passo",
		[]int{1, 2, 3, 4},
	},
	{
		"Brahms", "Sinfonia n. 3", "III mov. Poco allegretto",
		"Cagliari", "passo",
		[]int{4},
	},
	{
		"Mendelssohn", "Sinfonia n. 4 «Italiana»", "IV mov. Saltarello",
		"Roma: inizio–b.104, b.164–fine · Cagliari: IV mov.", "passo",
		[]int{1, 2, 4},
	},
	{
		"Mendelssohn", "Sinfonia n. 4 «Italiana»", "I mov.",
		"Santa Cecilia (chiede I–IV mov.)", "passo",
		[]int{1},
	},
	{
		"Mendelssohn", "Sogno di una notte di mezza estate", "n. 1 Scherzo",
		"San Carlo: da b.295 alla fine", "passo",
		[]int{2, 3},
	},
	{
		"Rossini", "Il barbiere di Siviglia", "Atto I, Finale",
		"Roma: da 2 b. prima del n.97 alla fine", "passo",
		[]int{2},
	},
	{
		"Rossini", "Guglielmo Tell", "Ouverture",
		"Roma: inizio–prima b. dell'Allegro · Cagliari: solo parte 2° violoncello", "passo",
		[]int{2, 4},
	},
	{
		"Čajkovskij", "Sinfonia n. 6", "II mov. Allegro con grazia",
		"Roma: estratto · San Carlo: dall'inizio (b.1–35+)", "passo",
		[]int{2, 3},
	},
	{
		"Čajkovskij", "Sinfonia n. 6", "III mov.",
		"Santa Cecilia", "passo",
		[]int{1},
	},
	{
		"Čajkovskij", "Sinfonia n. 4", "II mov.",
		"Santa Cecilia", "passo",
		[]int{1},
	},
	{
		"Verdi", "Messa da Requiem", "Offertorio",
		"Roma: n.3, inizio–b.62 · San Carlo: solo n.3 per vc e basso, dall'inizio", "passo",
		[]int{1, 2, 3, 4},
	},
	{
		"Verdi", "I vespri siciliani", "Sinfonia",
		"Roma: D→prima b. di E, seconda b. di H→prima b. di L · San Carlo: lett. di prova C, D, E · Cagliari: Ouverture", "passo",
		[]int{2, 3, 4},
	},
	{
		"Verdi", "Otello", "Atto I",
		"Cagliari (Secondo): da 17 battute dopo RR fino a SS · Cagliari (fila): solo parte del secondo", "passo",
		[]int{4},
	},
	{
		"Verdi", "Aida", "Atto III, duetto Aida-Amonasro",
		"San Carlo: parte di violoncello", "passo",
		[]int{3},
	},
	{
		"Puccini", "Tosca", "Atto III",
		"Roma: quartetto, inizio–prima b. di 11 · Cagliari (Secondo): solo parte 2° violoncello", "passo",
		[]int{2, 4},
	},
	{
		"Mahler", "Sinfonia n. 2", "I e II mov.",
		"Cagliari: I mov. fino al n.1; II mov. fino a 6 b. dopo il n.3, e da 7 b. dopo il n.5 fino al n.6", "passo",
		[]int{4},
	},
	{
		"Strauss", "Don Juan", "",
		"Santa Cecilia", "passo",
		[]int{1},
	},
	{
		"Šostakovič", "Sinfonia n. 5", "III mov.",
		"Santa Cecilia", "passo",
		[]int{1},
	},
	{
		"Prokofiev", "Romeo e Giulietta op. 64", "Andante",
		"San Carlo: da b.450", "passo",
		[]int{3},
	},
	// ---- solistici ----
	{
		"Haydn", "Concerto in Re maggiore", "I mov.",
		"Roma: CON cadenza (ed. Schott/Gendron) · San Carlo: senza cadenza (ed. Gevaert esclusa) · Santa Cecilia: senza cadenza, con pf · Cagliari (Secondo): anche II mov.", "solo",
		[]int{1, 2, 3, 4},
	},
	{
		"Haydn", "Concerto in Re maggiore", "II mov.",
		"Cagliari (Secondo violoncello)", "solo",
		[]int{4},
	},
	{
		"Haydn", "Concerto in Do maggiore", "I mov.",
		"San Carlo: eliminatoria a scelta fra Do e Re maggiore, senza cadenza", "solo",
		[]int{3},
	},
	{
		"Dvořák", "Concerto op. 104", "I mov.",
		"Cagliari ×2 · San Carlo: finale a scelta", "solo",
		[]int{3, 4},
	},
	{
		"Schumann", "Concerto", "I mov.",
		"San Carlo: finale a scelta fra Dvořák, Schumann, Elgar", "solo",
		[]int{3},
	},
	{
		"Elgar", "Concerto", "I mov.",
		"San Carlo: finale a scelta fra Dvořák, Schumann, Elgar", "solo",
		[]int{3},
	},
	{
		"Schubert", "Arpeggione", "I mov.",
		"Roma (eliminatoria)", "solo",
		[]int{2},
	},
	{
		"Bach", "Suite n. 2", "Preludio, Sarabanda, Giga",
		"Roma (eliminatoria)", "solo",
		[]int{2},
	},
	{
		"Piatti", "Capriccio n. 9 (op. 25)", "",
		"Roma (eliminatoria)", "solo",
		[]int{2},
	},
	{
		"Piatti", "Capriccio (op. 25)", "a scelta fra nn. 3, 6, 10, 12",
		"Santa Cecilia", "solo",
		[]int{1},
	},
	{
		"Hindemith", "Sonata per violoncello solo op. 25 n. 3", "",
		"Santa Cecilia", "solo",
		[]int{1},
	},
}

// seedIfEmpty inserts the concorsi and pieces above the first time the app runs
// with an empty database. It is idempotent: it does nothing when concorsi exist.
func seedIfEmpty(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM concorsi`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			log.Printf("seed rollback: %v", rerr)
		}
	}()
	ids := make([]int64, len(seedConcorsi))
	for i, c := range seedConcorsi {
		res, err := tx.Exec(`INSERT INTO concorsi(name, city, audition_date, weight)
			VALUES(?,?,?,?)`, c.name, c.city, c.date, c.weight)
		if err != nil {
			return fmt.Errorf("seed concorso %q: %w", c.name, err)
		}
		ids[i], err = res.LastInsertId()
		if err != nil {
			return err
		}
	}
	for _, p := range seedPieces {
		res, err := tx.Exec(`INSERT INTO pieces(composer, work, movement, excerpt, kind)
			VALUES(?,?,?,?,?)`, p.composer, p.work, p.movement, p.excerpt, p.kind)
		if err != nil {
			return fmt.Errorf("seed piece %q: %w", p.work, err)
		}
		pid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, ci := range p.concorsi {
			if _, err := tx.Exec(`INSERT INTO piece_concorso(piece_id, concorso_id) VALUES(?,?)`,
				pid, ids[ci-1]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
