package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var version = "dev"

var (
	flagAddr = flag.String("addr", ":8080", "adres i port nasłuchu")
	flagData = flag.String("data", "", "katalog danych (domyślnie: PRZYW_DATA albo folder 'dane' obok programu)")
)

func dataDir() string {
	if *flagData != "" {
		return *flagData
	}
	if d := os.Getenv("PRZYW_DATA"); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return "dane"
	}
	return filepath.Join(filepath.Dir(exe), "dane")
}

// runServer działa do anulowania ctx.
func runServer(ctx context.Context) error {
	dir := dataDir()
	store, created, err := OpenStore(dir)
	if err != nil {
		return err
	}
	if f, err := os.OpenFile(filepath.Join(dir, "serwer.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(multiWriter(os.Stderr, f))
	}
	log.Printf("Przywolania serwer %s, dane: %s, adres: %s", version, dir, *flagAddr)
	if created {
		log.Printf("PIERWSZE URUCHOMIENIE: haslo panelu /admin = %s, klucz urzadzen = %s (zapisane w %s)",
			store.Config.AdminPassword, store.Config.DeviceKey, filepath.Join(dir, "config.json"))
	}
	hub := NewHub(store, NewCallLog(dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler(hub, store))
	mux.HandleFunc("/zdrowie", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok", version) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/admin", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	adminRoutes(mux, hub, store)
	srv := &http.Server{Addr: *flagAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		return srv.Shutdown(sctx)
	case err := <-errc:
		return err
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Przywolania serwer %s\n\nUzycie:\n  przywolania-serwer [opcje]            uruchom w konsoli\n", version)
		serviceUsage()
		fmt.Fprintln(os.Stderr, "\nOpcje:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if handled, err := serviceMain(flag.Args()); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runServer(context.Background()); err != nil {
		log.Fatal(err)
	}
}
