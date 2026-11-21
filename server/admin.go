package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed admin.html
var adminHTML string

var adminTpl = template.Must(template.New("admin").Parse(adminHTML))

//go:embed panel.js
var panelJS string

//go:embed brygadzista.html
var foremanHTML string

var foremanTpl = template.Must(template.New("brygadzista").Parse(foremanHTML))

// 10 błędnych haseł z jednego adresu w 10 min = blokada panelu dla tego adresu na 15 min
var adminLimiter = NewLimiter(10, 10*time.Minute, 15*time.Minute)

func requireAdmin(store *Store, next http.HandlerFunc) http.HandlerFunc {
	return requirePassword(func() string { return store.Config.AdminPassword }, "Przywolania - panel", next)
}

func requirePassword(pass func() string, realm string, next http.HandlerFunc) http.HandlerFunc {
	return requirePasswords(func() []string { return []string{pass()} }, realm, next)
}

func passOK(given string, valid []string) bool {
	ok := false
	for _, v := range valid {
		if v != "" && subtle.ConstantTimeCompare([]byte(given), []byte(v)) == 1 {
			ok = true
		}
	}
	return ok
}

func requirePasswords(passes func() []string, realm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		ip := hostOf(r.RemoteAddr)
		if d := adminLimiter.Blocked(ip); d > 0 {
			h.Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
			http.Error(w, "Za dużo błędnych haseł. Spróbuj później.", http.StatusTooManyRequests)
			return
		}
		_, pass, ok := r.BasicAuth()
		if !ok || !passOK(pass, passes()) {
			if ok && adminLimiter.Fail(ip) {
				log.Printf("blokada panelu dla adresu %s (za dużo błędnych haseł)", ip)
			}
			h.Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
			http.Error(w, "Wymagane logowanie", http.StatusUnauthorized)
			return
		}
		adminLimiter.Success(ip)
		// ochrona przed CSRF: zmiany tylko z formularzy samego panelu
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			http.Error(w, "Odrzucono żądanie spoza panelu", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// sameOrigin: nagłówek Origin (albo Referer, gdy brak Origin) musi wskazywać na ten sam serwer.
func sameOrigin(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" || src == "null" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		return false
	}
	u, err := url.Parse(src)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func adminRoutes(mux *http.ServeMux, h *Hub, store *Store) {
	hub := h
	mux.HandleFunc("/admin", requireAdmin(store, func(w http.ResponseWriter, r *http.Request) {
		msg := ""
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			var err error
			switch r.FormValue("action") {
			case "save":
				err = store.UpsertPin(r.FormValue("id"), r.FormValue("name"), r.FormValue("pin"), r.FormValue("nopin") == "1", r.FormValue("foreman") == "1")
			case "delete":
				err = store.Delete(r.FormValue("id"))
			case "station":
				dev := r.FormValue("device")
				if err = store.SetStation(dev, r.FormValue("name")); err == nil {
					hub.StationChanged(dev, store.Station(dev))
					http.Redirect(w, r, "/admin", http.StatusSeeOther)
					return
				}
			}
			if err != nil {
				msg = err.Error()
			} else {
				h.WorkersChanged()
				http.Redirect(w, r, "/admin", http.StatusSeeOther)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = adminTpl.Execute(w, map[string]any{
			"Workers":   store.Workers(),
			"Tablets":   h.Tablets(),
			"DeviceKey": store.Config.DeviceKey,
			"Msg":       msg,
			"Version":   version,
		})
	}))
	mux.HandleFunc("/panel.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(panelJS))
	})
	mux.HandleFunc("/admin/stan", requireAdmin(store, liveState(h)))
	mux.HandleFunc("/admin/statystyki", requireAdmin(store, statsHandler(h)))

	foremanRoutes(mux, h, store)
	reportRoutes(mux, h, store)
}

func liveState(h *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workers":   h.State(),
			"tablets":   h.Tablets(),
			"terminals": h.Terminals(),
			"alerts":    h.Alerts(),
			"now":       time.Now().UnixMilli(),
		})
	}
}

func statsHandler(h *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to := StatsPeriod(r.URL.Query().Get("okres"), time.Now())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ComputeStats(h.calls.dir, from, to))
	}
}
