# studium

Diario di studio giornaliero per una violoncellista che prepara concorsi d'orchestra: ogni giorno calcola cosa studiare con una formula spiegabile, traccia minuti e confidenza per pezzo, e tiene lo storico.

## Esecuzione con docker

```sh
docker build -t studium .
docker run -d --name studium -p 8080:8080 \
  -e STUDIUM_PASSWORD='scegli-una-password' \
  -v studium-data:/data \
  ghcr.io/streambinder/studium:latest
```

Poi apri <http://localhost:8080> (utente predefinito `agnese`, password da `STUDIUM_PASSWORD`).

Variabili d'ambiente:

| variabile          | default  | descrizione                       |
| ------------------ | -------- | --------------------------------- |
| `STUDIUM_DATA_DIR` | `/data`  | directory del database SQLite     |
| `PORT`             | `8080`   | porta HTTP                        |
| `STUDIUM_USER`     | `agnese` | utente HTTP Basic Auth            |
| `STUDIUM_PASSWORD` | —        | **obbligatoria**; senza non parte |

Sviluppo locale: `go run .` con `STUDIUM_PASSWORD` impostata (richiede Go 1.24+).

## La formula del piano giornaliero

Per ogni pezzo, ogni giorno:

```text
punteggio = base × urgenza × bisogno × ripresa (× 1,5 se rimandato da ieri)
```

- **base** = somma dei pesi dei concorsi _futuri_ in cui compare il pezzo (i concorsi con data passata o archiviati non contano; un pezzo senza concorsi futuri esce dal piano)
- **urgenza** = 1 + k × max(0, 1 − giorni_alla_prova / orizzonte), con k=2 e orizzonte=60 giorni di default
- **bisogno** = 1 + (5 − confidenza) / 5, confidenza 1–5 dall'ultimo check-in (2,5 se mai valutata)
- **ripresa** = 1 + min(giorni_dall_ultima_seduta / 7, tetto), tetto=1 di default (spaced repetition)

I primi 8 pezzi per punteggio si dividono il budget del giorno (dalle fasce orarie libere dichiarate al mattino) in proporzione al punteggio: minimo 5 minuti, arrotondati ai 5. Ogni scheda mostra il conto completo, così è sempre chiaro _perché_ quel pezzo è lì oggi.

I coefficienti k, orizzonte e tetto si modificano da Impostazioni; i pesi dei concorsi da Concorsi.
