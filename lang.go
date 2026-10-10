package main

import (
	_ "embed"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/oschwald/maxminddb-golang"
)

// Language resolution, in order: an explicit choice stored in a
// cookie, the browser's Accept-Language, the country of the origin
// IP (only when the header names no supported language), English.

//go:embed geoip/country.mmdb
var geoipDB []byte

var (
	geoOnce   sync.Once
	geoReader *maxminddb.Reader
)

func langFor(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil && langSupported(c.Value) {
		return c.Value
	}
	if lang := acceptLanguage(r.Header.Get("Accept-Language")); lang != "" {
		return lang
	}
	if ip := clientIP(r); ip != nil {
		if lang := countryLanguage(ip); lang != "" {
			return lang
		}
	}
	return "en"
}

// acceptLanguage returns the best supported language of an
// Accept-Language header, or "" when it names none of them.
func acceptLanguage(header string) string {
	type pref struct {
		lang string
		q    float64
	}
	var prefs []pref
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, q := part, 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			for _, param := range strings.Split(part[i+1:], ";") {
				param = strings.TrimSpace(param)
				if strings.HasPrefix(param, "q=") {
					if v, err := strconv.ParseFloat(param[2:], 64); err == nil {
						q = v
					}
				}
			}
		}
		primary := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
		if langSupported(primary) {
			prefs = append(prefs, pref{primary, q})
		}
	}
	if len(prefs) == 0 {
		return ""
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	return prefs[0].lang
}

// clientIP returns the first public IP of the request chain: the
// proxy's X-Forwarded-For, then X-Real-IP, then the direct peer.
func clientIP(r *http.Request) net.IP {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, part := range strings.Split(xff, ",") {
			if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil && isPublicIP(ip) {
				return ip
			}
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if ip := net.ParseIP(xr); ip != nil && isPublicIP(ip) {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && isPublicIP(ip) {
		return ip
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

type geoRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// countryLanguage maps the country of an IP to one of the supported
// languages, or "" when the lookup is unavailable or unmapped.
func countryLanguage(ip net.IP) string {
	geoOnce.Do(func() {
		reader, err := maxminddb.FromBytes(geoipDB)
		if err != nil {
			log.Printf("geoip: database unavailable: %v", err)
			return
		}
		geoReader = reader
	})
	if geoReader == nil {
		return ""
	}
	var rec geoRecord
	if err := geoReader.Lookup(ip, &rec); err != nil {
		return ""
	}
	return countryToLang[rec.Country.ISOCode]
}

var countryToLang = map[string]string{
	"IT": "it", "SM": "it", "VA": "it", "CH": "de",
	"ES": "es", "MX": "es", "AR": "es", "BO": "es", "CL": "es", "CO": "es",
	"CR": "es", "CU": "es", "DO": "es", "EC": "es", "SV": "es", "GT": "es",
	"HN": "es", "NI": "es", "PA": "es", "PY": "es", "PE": "es", "PR": "es",
	"UY": "es", "VE": "es", "GQ": "es",
	"FR": "fr", "BE": "fr", "LU": "fr", "MC": "fr", "SN": "fr", "CI": "fr",
	"ML": "fr", "BF": "fr", "NE": "fr", "TG": "fr", "BJ": "fr", "GA": "fr",
	"CG": "fr", "CD": "fr", "CM": "fr", "MG": "fr", "HT": "fr",
	"DE": "de", "AT": "de", "LI": "de",
	"PT": "pt", "BR": "pt", "AO": "pt", "MZ": "pt", "CV": "pt", "GW": "pt",
	"GB": "en", "US": "en", "IE": "en", "AU": "en", "NZ": "en", "CA": "en",
	"ZA": "en", "NG": "en", "KE": "en", "GH": "en", "IN": "en", "SG": "en",
	"MT": "en", "JM": "en", "TT": "en", "BZ": "en", "GY": "en",
}

// langNames are the display names of the supported languages,
// written in each language itself.
var langNames = map[string]string{
	"en": "English",
	"it": "Italiano",
	"es": "Español",
	"fr": "Français",
	"de": "Deutsch",
	"pt": "Português",
}

// langChoice returns the explicit choice stored in the cookie, or
// "auto" when detection is in charge.
func langChoice(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil && langSupported(c.Value) {
		return c.Value
	}
	return "auto"
}

// handleSetLanguage stores the explicit language choice in a
// cookie; "auto" clears it and returns to detection.
func (a *App) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	choice := r.FormValue("lang")
	cookie := &http.Cookie{Name: "lang", Path: "/", SameSite: http.SameSiteLaxMode}
	switch {
	case choice == "auto":
		cookie.MaxAge = -1
	case langSupported(choice):
		cookie.Value = choice
		cookie.MaxAge = 365 * 24 * 3600
	default:
		http.Error(w, tr(langFor(r), "err.language"), http.StatusBadRequest)
		return
	}
	http.SetCookie(w, cookie)
	log.Printf("event language set value=%s", choice)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
