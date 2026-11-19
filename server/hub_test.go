package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type tc struct {
	t    *testing.T
	conn *websocket.Conn
}

func dial(t *testing.T, base string, q url.Values) *tc {
	t.Helper()
	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return &tc{t, conn}
}

func (c *tc) send(m map[string]any) {
	b, _ := json.Marshal(m)
	if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// wait czyta wiadomości aż trafi na typ t spełniający warunek.
func (c *tc) wait(typ string, ok func(map[string]any) bool) map[string]any {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, b, err := c.conn.Read(ctx)
		if err != nil {
			c.t.Fatalf("czekam na %s: %v", typ, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["t"] == typ && (ok == nil || ok(m)) {
			return m
		}
	}
}

func statusOf(m map[string]any, name string) string {
	for _, w := range m["workers"].([]any) {
		wm := w.(map[string]any)
		if wm["name"] == name {
			return wm["status"].(string)
		}
	}
	return ""
}

func TestFlow(t *testing.T) {
	store, _, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Upsert("", "Anna Nowak", "1234")
	_ = store.Upsert("", "Jan Kowalski", "")
	hub := NewHub(store, NewCallLog(store.dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler(hub, store))
	adminRoutes(mux, hub, store)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	key := store.Config.DeviceKey

	// zły klucz
	resp, _ := http.Get(srv.URL + "/ws?role=tablet&station=X&key=zly")
	if resp.StatusCode != 401 {
		t.Fatalf("zly klucz: %d", resp.StatusCode)
	}
	// panel bez hasła
	resp, _ = http.Get(srv.URL + "/admin")
	if resp.StatusCode != 401 {
		t.Fatalf("admin bez hasla: %d", resp.StatusCode)
	}

	term := dial(t, srv.URL, url.Values{"role": {"terminal"}, "key": {key}, "device": {"T1"}})
	wl := term.wait("workers", nil)
	var anna string
	for _, w := range wl["list"].([]any) {
		if w.(map[string]any)["name"] == "Anna Nowak" {
			anna = w.(map[string]any)["id"].(string)
		}
	}
	term.send(map[string]any{"t": "login", "worker": anna, "pin": "0000"})
	term.wait("login_err", nil)
	term.send(map[string]any{"t": "login", "worker": anna, "pin": "1234"})
	term.wait("login_ok", nil)

	tab1 := dial(t, srv.URL, url.Values{"role": {"tablet"}, "key": {key}, "station": {"Stanowisko 1"}, "device": {"A"}})
	tab1.wait("state", func(m map[string]any) bool { return statusOf(m, "Anna Nowak") == "dostepny" })
	tab2 := dial(t, srv.URL, url.Values{"role": {"tablet"}, "key": {key}, "station": {"Stanowisko 2"}, "device": {"B"}})
	tab2.wait("state", nil)

	tab1.send(map[string]any{"t": "call", "worker": anna})
	tab1.wait("call_result", func(m map[string]any) bool { return m["ok"] == true })
	call := term.wait("call", nil)
	if call["station"] != "STANOWISKO_1" {
		t.Fatalf("stanowisko: %v", call["station"])
	}
	tab2.wait("state", func(m map[string]any) bool { return statusOf(m, "Anna Nowak") == "wezwany" })

	// drugie stanowisko nie może przejąć niepotwierdzonego wezwania
	tab2.send(map[string]any{"t": "call", "worker": anna})
	tab2.wait("call_result", func(m map[string]any) bool { return m["ok"] == false })

	// rozłączenie i ponowne połączenie terminala: wezwanie wraca
	_ = term.conn.CloseNow()
	tab1.wait("state", func(m map[string]any) bool { return statusOf(m, "Anna Nowak") == "wezwany" })
	term = dial(t, srv.URL, url.Values{"role": {"terminal"}, "key": {key}, "device": {"T1"}})
	term.send(map[string]any{"t": "login", "worker": anna, "pin": "1234"})
	term.wait("login_ok", nil)
	call = term.wait("call", nil)

	term.send(map[string]any{"t": "ack", "id": call["id"]})
	tab1.wait("state", func(m map[string]any) bool { return statusOf(m, "Anna Nowak") == "idzie" })

	// anulowanie (+ podwójne dotknięcie nie tworzy drugiego wezwania)
	tab1.send(map[string]any{"t": "call", "worker": anna})
	c2 := term.wait("call", nil)
	tab1.send(map[string]any{"t": "call", "worker": anna})
	tab1.wait("call_result", nil)
	tab1.wait("call_result", nil)
	tab1.send(map[string]any{"t": "cancel", "worker": anna})
	term.wait("call_cancel", func(m map[string]any) bool { return m["id"] == c2["id"] })

	// wylogowanie: znika z tabletów
	term.send(map[string]any{"t": "logout"})
	tab2.wait("state", func(m map[string]any) bool { return statusOf(m, "Anna Nowak") == "" })
}

func TestStationAssignment(t *testing.T) {
	store, _, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Upsert("", "Anna Nowak", "")
	hub := NewHub(store, NewCallLog(store.dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler(hub, store))
	adminRoutes(mux, hub, store)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	key := store.Config.DeviceKey

	// tablet bez nazwy: łączy się, ale nie może wzywać
	tab := dial(t, srv.URL, url.Values{"role": {"tablet"}, "key": {key}, "device": {"T1"}})
	if m := tab.wait("station", nil); m["name"] != "" {
		t.Fatalf("oczekiwano pustego stołu: %v", m)
	}
	tab.send(map[string]any{"t": "call", "worker": store.Workers()[0].ID})
	tab.wait("call_result", func(m map[string]any) bool { return m["ok"] == false })

	// przypisanie w panelu dociera do połączonego tabletu
	form := url.Values{"action": {"station"}, "device": {"T1"}, "name": {"kontr 12"}}
	req, _ := http.NewRequest("POST", srv.URL+"/admin", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("x", store.Config.AdminPassword)
	// bez nagłówka Origin: odrzucone (CSRF)
	if resp, err := http.DefaultClient.Do(req.Clone(req.Context())); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf: %v %v", err, resp)
	}
	req, _ = http.NewRequest("POST", srv.URL+"/admin", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("x", store.Config.AdminPassword)
	req.Header.Set("Origin", srv.URL)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("panel: %v %v", err, resp)
	}
	tab.wait("station", func(m map[string]any) bool { return m["name"] == "KONTR_12" })

	// po ponownym połączeniu przypisanie z panelu wygrywa z nazwą wpisaną lokalnie
	tab2 := dial(t, srv.URL, url.Values{"role": {"tablet"}, "key": {key}, "device": {"T1"}, "station": {"INNY"}})
	tab2.wait("station", func(m map[string]any) bool { return m["name"] == "KONTR_12" })
	if got := store.Station("T1"); got != "KONTR_12" {
		t.Fatalf("zapis: %q", got)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Now()
	l := NewLimiter(3, time.Minute, 5*time.Minute)
	l.now = func() time.Time { return now }
	l.Fail("a")
	l.Fail("a")
	if l.Blocked("a") > 0 {
		t.Fatal("za wcześnie zablokowany")
	}
	if !l.Fail("a") || l.Blocked("a") == 0 {
		t.Fatal("powinien być zablokowany po 3 próbach")
	}
	l.Success("a") // w trakcie blokady sukces jej nie zdejmuje
	if l.Blocked("a") == 0 {
		t.Fatal("blokada zdjęta za wcześnie")
	}
	now = now.Add(6 * time.Minute)
	if l.Blocked("a") > 0 {
		t.Fatal("blokada powinna wygasnąć")
	}
}

func TestPinLimit(t *testing.T) {
	store, _, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Upsert("", "Ewa Test", "4321")
	id := store.Workers()[0].ID
	hub := NewHub(store, NewCallLog(store.dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler(hub, store))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	term := dial(t, srv.URL, url.Values{"role": {"terminal"}, "key": {store.Config.DeviceKey}, "device": {"P1"}})
	for i := 0; i < 5; i++ {
		term.send(map[string]any{"t": "login", "worker": id, "pin": "0000"})
		term.wait("login_err", nil)
	}
	// po 5 błędach nawet dobry PIN jest odrzucony
	term.send(map[string]any{"t": "login", "worker": id, "pin": "4321"})
	m := term.wait("login_err", nil)
	if !strings.Contains(m["reason"].(string), "Za dużo") {
		t.Fatalf("oczekiwano blokady: %v", m)
	}
}

func TestCert(t *testing.T) {
	dir := t.TempDir()
	crt, _, err := ensureCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(crt, filepath.Join(dir, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(pair.Certificate[0])
	if !strings.HasPrefix(SPKIPin(c), "sha256/") || c.NotAfter.Before(time.Now().AddDate(9, 0, 0)) {
		t.Fatalf("certyfikat: %v %s", c.NotAfter, SPKIPin(c))
	}
}
