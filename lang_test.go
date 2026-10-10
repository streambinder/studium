package main

import (
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestAcceptLanguageParsing(t *testing.T) {
	cases := map[string]string{
		"it-IT,it;q=0.9,en;q=0.8":  "it",
		"en-US":                    "en",
		"de-DE,de;q=0.9":           "de",
		"fr;q=0.5,it;q=0.9":        "it",
		"ja-JP,ja;q=0.9":           "",
		"es-ES,en;q=0.1":           "es",
		"pt-BR":                    "pt",
		"":                         "",
		"nl-NL,fr-BE;q=0.7":        "fr",
		"en;q=0,it;q=0.5":          "it", // q=0 still listed; order by q puts it first anyway
		"zh-CN,pt-PT;q=0.8,de;q=0": "pt",
	}
	for header, want := range cases {
		if got := acceptLanguage(header); got != want {
			t.Errorf("acceptLanguage(%q): want %q, got %q", header, want, got)
		}
	}
}

// Resolution order: cookie, then Accept-Language, then the
// country of the origin IP, then English.
func TestLangForPriority(t *testing.T) {
	req := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	italianIP := map[string]string{"X-Forwarded-For": "151.99.1.1"}
	// Cookie wins over everything.
	r := req(map[string]string{"Accept-Language": "en", "X-Forwarded-For": "151.99.1.1"})
	r.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
	if got := langFor(r); got != "de" {
		t.Errorf("cookie: want de, got %q", got)
	}
	// Header wins over the IP country.
	r = req(map[string]string{"Accept-Language": "fr", "X-Forwarded-For": "151.99.1.1"})
	if got := langFor(r); got != "fr" {
		t.Errorf("header over IP: want fr, got %q", got)
	}
	// The IP speaks only when the header names no supported language.
	r = req(map[string]string{"Accept-Language": "ja", "X-Forwarded-For": "151.99.1.1"})
	if got := langFor(r); got != "it" {
		t.Errorf("IP fallback: want it, got %q", got)
	}
	// No signals at all: English.
	r = req(italianIP)
	r.Header.Del("X-Forwarded-For")
	if got := langFor(r); got != "en" {
		t.Errorf("default: want en, got %q", got)
	}
	// An invalid cookie value is ignored.
	r = req(map[string]string{"Accept-Language": "es"})
	r.AddCookie(&http.Cookie{Name: "lang", Value: "xx"})
	if got := langFor(r); got != "es" {
		t.Errorf("invalid cookie: want es, got %q", got)
	}
}

func TestCountryLanguageLookup(t *testing.T) {
	cases := map[string]string{
		"151.99.1.1":   "it", // Telecom Italia
		"217.70.184.1": "fr", // Gandi, France
		"200.160.0.5":  "pt", // Brazil
		"8.8.8.8":      "en", // Google, US
	}
	for ip, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-For", ip)
		if got := langFor(r); got != want {
			t.Errorf("IP %s: want %q, got %q", ip, want, got)
		}
	}
	// Private and loopback addresses never resolve a country.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "192.168.1.10, 10.0.0.4")
	if got := langFor(r); got != "en" {
		t.Errorf("private chain: want en, got %q", got)
	}
}

// A page renders in the detected language end to end.
func TestHomeRendersDetectedLanguage(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	a := &App{db: db}
	mux := http.NewServeMux()
	a.routes(mux)
	get := func(header string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Accept-Language", header)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if body := get("it"); !strings.Contains(body, "Piano di studio") {
		t.Fatal("italian home missing its hero")
	}
	if body := get(""); !strings.Contains(body, "Study plan") {
		t.Fatal("english home missing its hero")
	}
}

// CSS geometry must keep dot decimals in every language: a localized
// comma inside calc() invalidates the declaration and collapses the
// preparation map tiles to zero size.
func TestDiaryTileGeometryUsesDotDecimalsInItalian(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	stmts := []string{
		`INSERT INTO pieces(id, composer, work, kind, difficulty) VALUES(1, 'Alfa', 'Pezzo', 'excerpt', 3)`,
		`INSERT INTO sessions(date, piece_id, minutes, confidence, note) VALUES('2026-10-05', 1, 30, 4, '')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := &App{db: db}
	req := httptest.NewRequest(http.MethodGet, "/diary", nil)
	req.Header.Set("Accept-Language", "it")
	rec := httptest.NewRecorder()
	a.handleDiary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diary status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "calc(0.0% + 3px)") {
		t.Error("tile geometry lacks dot decimals in Italian rendering")
	}
	if strings.Contains(body, ",0%") || strings.Contains(body, ",6%") {
		t.Error("tile geometry contains comma decimals in Italian rendering")
	}
}

func TestLangChoiceAndSetLanguage(t *testing.T) {
	_, mux, _ := covApp(t)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	if got := langChoice(req); got != "auto" {
		t.Errorf("langChoice without cookie: want auto, got %q", got)
	}
	req.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
	if got := langChoice(req); got != "de" {
		t.Errorf("langChoice with cookie: want de, got %q", got)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req2.AddCookie(&http.Cookie{Name: "lang", Value: "xx"})
	if got := langChoice(req2); got != "auto" {
		t.Errorf("langChoice with invalid cookie: want auto, got %q", got)
	}

	code, _, hdr := covPost(t, mux, "/settings/language", url.Values{"lang": {"it"}})
	if code != http.StatusSeeOther || hdr.Get("Location") != "/settings" {
		t.Fatalf("set language: want 303 to /settings, got %d", code)
	}
	if sc := hdr.Get("Set-Cookie"); !strings.Contains(sc, "lang=it") {
		t.Errorf("set language cookie: got %q", sc)
	}
	code, _, hdr = covPost(t, mux, "/settings/language", url.Values{"lang": {"auto"}})
	if code != http.StatusSeeOther {
		t.Fatalf("set language auto: want 303, got %d", code)
	}
	if sc := hdr.Get("Set-Cookie"); !strings.Contains(sc, "Max-Age=0") {
		t.Errorf("set language auto must clear the cookie: got %q", sc)
	}
	if code, _, _ := covPost(t, mux, "/settings/language", url.Values{"lang": {"xx"}}); code != http.StatusBadRequest {
		t.Errorf("set language invalid: want 400, got %d", code)
	}
	if code, _, _ := covPost(t, mux, "/settings/language", url.Values{}); code != http.StatusBadRequest {
		t.Errorf("set language missing: want 400, got %d", code)
	}
}

func TestClientIPBranches(t *testing.T) {
	build := func(xff, realIP, remote string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if realIP != "" {
			r.Header.Set("X-Real-IP", realIP)
		}
		r.RemoteAddr = remote
		return r
	}
	cases := []struct {
		name                string
		xff, realIP, remote string
		want                string
	}{
		{"xff first public", "8.8.8.8, 10.0.0.1", "", "192.168.1.1:80", "8.8.8.8"},
		{"xff garbage then public", "garbage, 1.1.1.1", "", "192.168.1.1:80", "1.1.1.1"},
		{"xff private only, real ip", "10.0.0.1", "9.9.9.9", "192.168.1.1:80", "9.9.9.9"},
		{"real ip invalid, remote", "", "garbage", "9.9.9.9:1234", "9.9.9.9"},
		{"remote without port", "", "", "9.9.9.9", "9.9.9.9"},
		{"all private", "10.0.0.1", "192.168.0.2", "172.16.0.5:80", ""},
		{"remote invalid", "", "", "garbage", ""},
	}
	for _, c := range cases {
		ip := clientIP(build(c.xff, c.realIP, c.remote))
		got := ""
		if ip != nil {
			got = ip.String()
		}
		if got != c.want {
			t.Errorf("%s: want %q, got %q", c.name, c.want, got)
		}
	}
	if got := countryLanguage(net.ParseIP("151.99.1.1")); got != "it" {
		t.Errorf("countryLanguage italian IP: got %q", got)
	}
	if got := countryLanguage(net.ParseIP("127.0.0.1")); got != "" {
		t.Errorf("countryLanguage loopback: want empty, got %q", got)
	}
	if got := countryLanguage(net.IP{1, 2, 3}); got != "" {
		t.Errorf("countryLanguage malformed IP: want empty, got %q", got)
	}
	if got := countryLanguage(net.IP{}); got != "" {
		t.Errorf("countryLanguage empty IP: want empty, got %q", got)
	}
}
