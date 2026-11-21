package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Panel brygadzisty: logowanie imieniem i nazwiskiem + PIN-em z terminala.
// Wpuszcza tylko pracowników z zaznaczonym "Brygadzista" i ustawionym PIN-em.
// Sesja w ciasteczku podpisanym kluczem losowanym przy starcie serwera (restart = ponowne logowanie);
// podpis obejmuje PIN, więc zmiana PIN-u albo odznaczenie brygadzisty kończy sesję od razu.

const foremanCookie = "przyw_bryg"
const foremanSession = 12 * time.Hour

var sessionKey = func() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}()

//go:embed brygadzista-logowanie.html
var foremanLoginHTML string

var foremanLoginTpl = template.Must(template.New("logowanie").Parse(foremanLoginHTML))

func signForeman(id, pin string, exp int64) string {
	m := hmac.New(sha256.New, sessionKey)
	fmt.Fprintf(m, "%s|%d|%s", id, exp, pin)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// foremanFromRequest zwraca nazwę zalogowanego brygadzisty (pusta = brak ważnej sesji).
func foremanFromRequest(store *Store, r *http.Request) (id, name string) {
	c, err := r.Cookie(foremanCookie)
	if err != nil {
		return "", ""
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return "", ""
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", ""
	}
	n, pin, ok := store.foremanSecret(parts[0])
	if !ok || !hmac.Equal([]byte(parts[2]), []byte(signForeman(parts[0], pin, exp))) {
		return "", ""
	}
	return parts[0], n
}

func isAdminRequest(store *Store, r *http.Request) bool {
	_, pass, ok := r.BasicAuth()
	return ok && passOK(pass, []string{store.Config.AdminPassword})
}

func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
}

// requireForeman: ważna sesja brygadzisty albo hasło administratora (przeglądarka z otwartym /admin).
func requireForeman(store *Store, next func(w http.ResponseWriter, r *http.Request, who string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Tylko podgląd", http.StatusMethodNotAllowed)
			return
		}
		if _, name := foremanFromRequest(store, r); name != "" {
			next(w, r, name)
			return
		}
		if isAdminRequest(store, r) {
			next(w, r, "administrator")
			return
		}
		if r.URL.Path == "/brygadzista" {
			http.Redirect(w, r, "/brygadzista/logowanie", http.StatusSeeOther)
			return
		}
		http.Error(w, "Wymagane logowanie", http.StatusUnauthorized)
	}
}

func foremanRoutes(mux *http.ServeMux, h *Hub, store *Store) {
	mux.HandleFunc("/brygadzista", requireForeman(store, func(w http.ResponseWriter, r *http.Request, who string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = foremanTpl.Execute(w, map[string]any{"Version": version, "Who": who})
	}))
	mux.HandleFunc("/brygadzista/stan", requireForeman(store, func(w http.ResponseWriter, r *http.Request, _ string) { liveState(h)(w, r) }))
	mux.HandleFunc("/brygadzista/statystyki", requireForeman(store, func(w http.ResponseWriter, r *http.Request, _ string) { statsHandler(h)(w, r) }))

	mux.HandleFunc("/brygadzista/logowanie", func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		msg := ""
		sel := ""
		if r.Method == http.MethodPost {
			if !sameOrigin(r) {
				http.Error(w, "Odrzucono żądanie spoza panelu", http.StatusForbidden)
				return
			}
			_ = r.ParseForm()
			id, pin := r.FormValue("id"), r.FormValue("pin")
			sel = id
			ip := hostOf(r.RemoteAddr)
			wKey := "w:" + id
			if d := max(adminLimiter.Blocked(ip), pinLimiter.Blocked(wKey)); d > 0 {
				msg = fmt.Sprintf("Za dużo błędnych prób. Spróbuj za %d min.", int(d.Minutes())+1)
			} else if !store.ForemanLogin(id, pin) {
				pinLimiter.Fail(wKey)
				if adminLimiter.Fail(ip) {
					log.Printf("blokada panelu brygadzisty dla adresu %s (za dużo błędnych PIN-ów)", ip)
				}
				msg = "Nieprawidłowy PIN albo osoba nie jest brygadzistą."
			} else {
				pinLimiter.Success(wKey)
				_, p, _ := store.foremanSecret(id)
				exp := time.Now().Add(foremanSession).Unix()
				http.SetCookie(w, &http.Cookie{
					Name: foremanCookie, Value: id + "." + strconv.FormatInt(exp, 10) + "." + signForeman(id, p, exp),
					Path: "/brygadzista", MaxAge: int(foremanSession.Seconds()), HttpOnly: true,
					Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
				})
				log.Printf("panel brygadzisty: zalogowano %s z %s", store.WorkerName(id), ip)
				http.Redirect(w, r, "/brygadzista", http.StatusSeeOther)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = foremanLoginTpl.Execute(w, map[string]any{"Foremen": store.Foremen(), "Msg": msg, "Sel": sel})
	})

	mux.HandleFunc("/brygadzista/wyloguj", func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		http.SetCookie(w, &http.Cookie{Name: foremanCookie, Value: "", Path: "/brygadzista", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/brygadzista/logowanie", http.StatusSeeOther)
	})
}
