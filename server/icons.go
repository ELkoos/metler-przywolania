package main

import (
	"embed"
	"fmt"
	"net/http"
	"path"
)

// Ikona strony i aplikacji (PWA). Pliki publiczne: przeglądarka pobiera manifest
// i ikony bez hasła, więc nie mogą być za logowaniem. Nie zawierają żadnych danych.
//
//go:embed ikony/*
var iconFS embed.FS

var iconTypes = map[string]string{
	".png": "image/png",
	".svg": "image/svg+xml",
	".ico": "image/x-icon",
}

func manifest(id, name, short, start, theme string) string {
	return fmt.Sprintf(`{
  "id": %q,
  "name": %q,
  "short_name": %q,
  "start_url": %q,
  "scope": %q,
  "display": "standalone",
  "background_color": "#f4f5f7",
  "theme_color": %q,
  "lang": "pl",
  "icons": [
    {"src": "/ikony/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
    {"src": "/ikony/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
    {"src": "/ikony/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
    {"src": "/ikony/icon.svg", "sizes": "any", "type": "image/svg+xml", "purpose": "any"}
  ]
}
`, id, name, short, start, start, theme)
}

var manifests = map[string]string{
	"/manifest-admin.webmanifest":       manifest("/admin", "Przywołania – panel administratora", "Przywołania", "/admin", "#1d1d1f"),
	"/manifest-brygadzista.webmanifest": manifest("/brygadzista", "Przywołania – panel brygadzisty", "Brygadzista", "/brygadzista", "#1559c7"),
}

func iconRoutes(mux *http.ServeMux) {
	serve := func(w http.ResponseWriter, name string) {
		b, err := iconFS.ReadFile("ikony/" + name)
		if err != nil {
			http.Error(w, "404", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", iconTypes[path.Ext(name)])
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	}
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { serve(w, "favicon.ico") })
	mux.HandleFunc("/apple-touch-icon.png", func(w http.ResponseWriter, r *http.Request) { serve(w, "apple-touch-icon.png") })
	mux.HandleFunc("/ikony/", func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		if _, ok := iconTypes[path.Ext(name)]; !ok || name != r.URL.Path[len("/ikony/"):] {
			http.NotFound(w, r)
			return
		}
		serve(w, name)
	})
	for p, body := range manifests {
		body := body
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(body))
		})
	}
}
