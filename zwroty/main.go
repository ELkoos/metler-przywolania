package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var version = "dev"

var (
	flagAddr = flag.String("addr", ":8081", "adres i port nasłuchu")
	flagData = flag.String("data", "", "katalog danych (domyślnie: ZWROTY_DATA albo folder 'dane' obok programu)")
)

func dataDir() string {
	if *flagData != "" {
		return *flagData
	}
	if d := os.Getenv("ZWROTY_DATA"); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return "dane"
	}
	return filepath.Join(filepath.Dir(exe), "dane")
}

func main() {
	flag.Parse()
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	if f, err := os.OpenFile(filepath.Join(dir, "serwer.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	cfg, created, err := OpenConfig(dir)
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Zwroty %s, dane: %s, adres: %s", version, dir, *flagAddr)
	if created {
		log.Printf("PIERWSZE URUCHOMIENIE: haslo panelu /admin = %s (zapisane w %s)", cfg.Get().AdminPassword, filepath.Join(dir, "config.json"))
	}
	syncer := NewSyncer(cfg, store, NewAllegro(cfg), NewBase(cfg))
	go syncer.Run(context.Background())

	mux := http.NewServeMux()
	routes(mux, cfg, store, syncer)
	mux.HandleFunc("/zdrowie", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok", version) })
	srv := &http.Server{Addr: *flagAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
