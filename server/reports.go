package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

//go:embed raporty.html
var reportsHTML string

var reportsTpl = template.Must(template.New("raporty").Parse(reportsHTML))

var logFileRe = regexp.MustCompile(`^zdarzenia-(\d{4}-\d{2})\.csv$`)

type logFile struct {
	Name, Month, Size string
}

func listLogs(dir string) []logFile {
	entries, _ := os.ReadDir(dir)
	out := []logFile{}
	for _, e := range entries {
		m := logFileRe.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		size := ""
		if fi, err := e.Info(); err == nil {
			size = fmt.Sprintf("%.1f kB", float64(fi.Size())/1024)
		}
		out = append(out, logFile{Name: e.Name(), Month: m[1], Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out
}

func reportRoutes(mux *http.ServeMux, h *Hub, store *Store) {
	mux.HandleFunc("/admin/raporty", requireAdmin(store, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = reportsTpl.Execute(w, map[string]any{"Logs": listLogs(h.calls.dir)})
	}))
	mux.HandleFunc("/admin/dziennik", requireAdmin(store, func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("plik")
		if !logFileRe.MatchString(name) { // tylko pliki dziennika, bez ścieżek
			http.NotFound(w, r)
			return
		}
		h.calls.mu.Lock() // spójny odczyt (bez dopisywania w trakcie)
		b, err := os.ReadFile(filepath.Join(h.calls.dir, name))
		h.calls.mu.Unlock()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		_, _ = w.Write(b)
	}))
}
