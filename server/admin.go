package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
)

//go:embed admin.html
var adminHTML string

var adminTpl = template.Must(template.New("admin").Parse(adminHTML))

func requireAdmin(store *Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(store.Config.AdminPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Przywolania - panel", charset="UTF-8"`)
			http.Error(w, "Wymagane logowanie", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func adminRoutes(mux *http.ServeMux, h *Hub, store *Store) {
	mux.HandleFunc("/admin", requireAdmin(store, func(w http.ResponseWriter, r *http.Request) {
		msg := ""
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			var err error
			switch r.FormValue("action") {
			case "save":
				err = store.Upsert(r.FormValue("id"), r.FormValue("name"), r.FormValue("pin"))
			case "delete":
				err = store.Delete(r.FormValue("id"))
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
			"DeviceKey": store.Config.DeviceKey,
			"Msg":       msg,
			"Version":   version,
		})
	}))
	mux.HandleFunc("/admin/stan", requireAdmin(store, func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		tablets := []map[string]string{}
		terminals := 0
		for c := range h.clients {
			if c.Role == "tablet" {
				tablets = append(tablets, map[string]string{"station": c.Station, "device": c.Device, "ip": c.Remote})
			} else {
				terminals++
			}
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"workers": h.State(), "tablets": tablets, "terminals": terminals})
	}))
}
