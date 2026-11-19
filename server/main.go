package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var version = "dev"

var (
	flagAddr    = flag.String("addr", ":8080", "adres i port nasłuchu HTTP (pusty = wyłączony)")
	flagTLSAddr = flag.String("tls-addr", ":8443", "adres i port nasłuchu HTTPS/WSS (pusty = wyłączony); certyfikat dane/tls.crt + tls.key")
	flagData    = flag.String("data", "", "katalog danych (domyślnie: PRZYW_DATA albo folder 'dane' obok programu)")
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
	log.Printf("Przywolania serwer %s, dane: %s, HTTP: %s, HTTPS: %s", version, dir, *flagAddr, *flagTLSAddr)
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
	var servers []*http.Server
	errc := make(chan error, 2)
	if *flagAddr != "" {
		srv := &http.Server{Addr: *flagAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go func() { errc <- srv.ListenAndServe() }()
	}
	if *flagTLSAddr != "" {
		crt, key, err := ensureCert(dir)
		if err != nil {
			return err
		}
		srv := &http.Server{Addr: *flagTLSAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second,
			TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
		servers = append(servers, srv)
		go func() { errc <- srv.ListenAndServeTLS(crt, key) }()
	}
	if len(servers) == 0 {
		return fmt.Errorf("oba porty wyłączone (-addr i -tls-addr puste)")
	}
	select {
	case <-ctx.Done():
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		for _, s := range servers {
			_ = s.Shutdown(sctx)
		}
		return nil
	case err := <-errc:
		return err
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Przywolania serwer %s\n\nUzycie:\n  przywolania-serwer [opcje]            uruchom w konsoli\n  przywolania-serwer cert ADRES-IP [folder]  certyfikat TLS + pin dla aplikacji\n", version)
		serviceUsage()
		fmt.Fprintln(os.Stderr, "\nOpcje:")
		flag.PrintDefaults()
	}
	flag.Parse()
	// przywolania-serwer cert 192.168.0.244 [folder]: certyfikat TLS dla podanego adresu i pin dla aplikacji
	if a := flag.Args(); len(a) >= 2 && a[0] == "cert" {
		dir := "."
		if len(a) >= 3 {
			dir = a[2]
		}
		ip := net.ParseIP(a[1])
		if ip == nil {
			log.Fatalf("niepoprawny adres IP: %s", a[1])
		}
		crt, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
		if err := generateCert(crt, key, []net.IP{ip, net.ParseIP("127.0.0.1")}); err != nil {
			log.Fatal(err)
		}
		pair, err := tls.LoadX509KeyPair(crt, key)
		if err != nil {
			log.Fatal(err)
		}
		c, _ := x509.ParseCertificate(pair.Certificate[0])
		fmt.Printf("%s\n%s\npin: %s\n", crt, key, SPKIPin(c))
		return
	}
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
