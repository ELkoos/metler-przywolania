package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed index.html
var indexHTML []byte

//go:embed admin.html
var adminHTML string

var adminTpl = template.Must(template.New("admin").Parse(adminHTML))

func writeJSONResp(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSONResp(w, code, map[string]string{"error": msg})
}

func requireAdmin(cfg *ConfigStore, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.Get().AdminPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Zwroty - panel", charset="UTF-8"`)
			http.Error(w, "Wymagane logowanie", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func routes(mux *http.ServeMux, cfg *ConfigStore, store *Store, syncer *Syncer) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})

	mux.HandleFunc("POST /api/skan", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code, Batch, Worker string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			errJSON(w, 400, "zły format")
			return
		}
		if strings.TrimSpace(in.Worker) == "" {
			errJSON(w, 400, "wybierz osobę skanującą")
			return
		}
		code := NormCode(in.Code)
		store.mu.RLock()
		a, b, _ := store.matchLocked(code)
		store.mu.RUnlock()
		if a == "" && b == 0 && code != "" {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			syncer.LookupLive(ctx, code)
			cancel()
		}
		sc, err := store.AddScan(in.Code, in.Batch, in.Worker, time.Now())
		if err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		v, _ := store.ScanView(sc.ID)
		writeJSONResp(w, 200, v)
	})

	mux.HandleFunc("GET /api/skany", func(w http.ResponseWriter, r *http.Request) {
		d, _ := strconv.Atoi(r.URL.Query().Get("dni"))
		if d <= 0 {
			d = 1
		}
		writeJSONResp(w, 200, store.Scans(startOfDay(time.Now()).AddDate(0, 0, -(d-1)), false))
	})

	mux.HandleFunc("GET /api/wyjasnienia", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, 200, store.Scans(time.Time{}, true))
	})

	mux.HandleFunc("POST /api/wyjasnij", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Ref, Note string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			errJSON(w, 400, "zły format")
			return
		}
		if strings.TrimSpace(in.Ref) == "" && strings.TrimSpace(in.Note) == "" {
			errJSON(w, 400, "wpisz numer zwrotu albo notatkę")
			return
		}
		if err := store.ResolveScan(in.ID, in.Ref, in.Note); err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		writeJSONResp(w, 200, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/usun-skan", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if err := store.DeleteScan(in.ID); err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		writeJSONResp(w, 200, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/alerty", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, 200, store.Alerts(cfg.Get(), time.Now()))
	})

	mux.HandleFunc("GET /api/stan", func(w http.ResponseWriter, r *http.Request) {
		c := cfg.Get()
		store.mu.RLock()
		defer store.mu.RUnlock()
		accounts := []string{}
		for _, a := range c.AllegroAccounts {
			accounts = append(accounts, a.Name)
		}
		errs := map[string]string{}
		for k, v := range store.st.SyncErrors {
			errs[k] = v
		}
		writeJSONResp(w, 200, map[string]any{
			"version": version, "workers": c.Workers, "allegroAccounts": accounts,
			"baseOn": c.BaseToken != "", "lastAllegro": store.st.LastAllegroSync, "lastBase": store.st.LastBaseSync,
			"allegroCount": len(store.st.Allegro), "baseCount": len(store.st.Base), "errors": errs,
			"deadlineDays": c.DeadlineDays,
		})
	})

	mux.HandleFunc("GET /eksport.csv", func(w http.ResponseWriter, r *http.Request) {
		d, _ := strconv.Atoi(r.URL.Query().Get("dni"))
		if d <= 0 {
			d = 30
		}
		list := store.Scans(startOfDay(time.Now()).AddDate(0, 0, -(d-1)), false)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="skany-zwrotow.csv"`)
		_, _ = w.Write([]byte("\xEF\xBB\xBF"))
		cw := csv.NewWriter(w)
		cw.Comma = ';'
		_ = cw.Write([]string{"Data", "Osoba", "Partia", "Numer paczki", "Dopasowanie", "Konto Allegro", "Nr zwrotu Allegro",
			"Zamówienie Allegro", "Status Allegro", "Zwrot środków", "Nr zwrotu Base", "Zamówienie Base", "Status Base", "Uwagi"})
		for _, v := range list {
			row := []string{v.At.Format("2006-01-02 15:04"), v.Worker, v.Batch, v.Raw, v.Match, "", "", "", "", "", "", "", v.BaseStatus, v.Note}
			if v.Allegro != nil {
				row[5], row[6], row[7], row[8] = v.Allegro.Account, v.Allegro.ReferenceNumber, v.Allegro.OrderID, v.Allegro.Status
				if v.Allegro.Refund != nil && v.Allegro.Refund.Success {
					row[9] = v.Allegro.Refund.Amount + " " + v.Allegro.Refund.Currency
				}
			}
			if v.Base != nil {
				row[10], row[11] = v.Base.ReferenceNumber, strconv.FormatInt(v.Base.OrderID, 10)
			}
			if !v.Matched() && !v.Resolved {
				row[4] = "DO WYJAŚNIENIA"
			}
			_ = cw.Write(row)
		}
		cw.Flush()
	})

	mux.HandleFunc("/admin", requireAdmin(cfg, func(w http.ResponseWriter, r *http.Request) {
		msg := ""
		var device *pendingDevice
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			var err error
			switch r.FormValue("action") {
			case "ustawienia":
				err = cfg.Update(func(c *Config) {
					c.AllegroClientID = strings.TrimSpace(r.FormValue("clientId"))
					if s := strings.TrimSpace(r.FormValue("clientSecret")); s != "" {
						c.AllegroClientSecret = s
					}
					if s := strings.TrimSpace(r.FormValue("baseToken")); s != "" {
						c.BaseToken = s
					}
					if r.FormValue("baseOff") == "1" {
						c.BaseToken = ""
					}
					c.Workers = nil
					for _, l := range strings.Split(r.FormValue("workers"), "\n") {
						if l = strings.TrimSpace(l); l != "" {
							c.Workers = append(c.Workers, l)
						}
					}
					atoi := func(k string) int { n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(k))); return n }
					c.SyncMinutes = atoi("syncMinutes")
					c.HistoryDays = atoi("historyDays")
					c.RefundAlertDays = atoi("refundAlertDays")
					c.DeadlineDays = atoi("deadlineDays")
					c.DeadlineFrom = r.FormValue("deadlineFrom")
					c.DeadlineWarnDays = atoi("deadlineWarnDays")
					c.NotScannedDays = atoi("notScannedDays")
				})
				if err == nil {
					msg = "Zapisano."
				}
			case "dodaj-konto":
				name := strings.TrimSpace(r.FormValue("name"))
				if name == "" {
					msg = "Podaj nazwę konta."
					break
				}
				device, err = syncer.allegro.StartDevice(r.Context(), name)
			case "usun-konto":
				name := r.FormValue("name")
				err = cfg.Update(func(c *Config) {
					out := c.AllegroAccounts[:0]
					for _, a := range c.AllegroAccounts {
						if a.Name != name {
							out = append(out, a)
						}
					}
					c.AllegroAccounts = out
				})
			case "synchronizuj":
				syncer.Kick()
				msg = "Synchronizacja uruchomiona."
			}
			if err != nil {
				msg = err.Error()
			}
		}
		c := cfg.Get()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = adminTpl.Execute(w, map[string]any{"C": c, "Msg": msg, "Device": device, "Version": version,
			"SecretSet": c.AllegroClientSecret != "", "BaseSet": c.BaseToken != ""})
	}))

	mux.HandleFunc("GET /admin/konto-stan", requireAdmin(cfg, func(w http.ResponseWriter, r *http.Request) {
		st, msg := DeviceState(r.URL.Query().Get("kod"))
		writeJSONResp(w, 200, map[string]string{"state": st, "msg": msg})
	}))
}
