package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	_ = store.Upsert("", "Anna Nowak", "1234", false)
	_ = store.Upsert("", "Jan Kowalski", "", false)
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
	_ = store.Upsert("", "Anna Nowak", "", false)
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
	_ = store.Upsert("", "Ewa Test", "4321", false)
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

func TestAllowList(t *testing.T) {
	a, err := ParseAllowList("192.168.0.0/23")
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{
		"192.168.0.244:5000": true, "192.168.1.255:1": true, "192.168.2.1:1": false,
		"10.0.0.5:1": false, "127.0.0.1:1": true, "[::1]:1": true, "172.17.0.1:1": false,
	} {
		if got := a.Allowed(addr); got != want {
			t.Errorf("%s: %v, oczekiwano %v", addr, got, want)
		}
	}
	none, _ := ParseAllowList("")
	if !none.Allowed("8.8.8.8:1") {
		t.Error("pusta lista powinna przepuszczać wszystko")
	}
	if _, err := ParseAllowList("192.168.0.0/99"); err == nil {
		t.Error("oczekiwano błędu")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/zdrowie", nil)
	req.RemoteAddr = "203.0.113.9:4000"
	a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("kod %d", rec.Code)
	}
}

func TestEscalation(t *testing.T) {
	store, _, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Upsert("", "Anna Nowak", "", false)
	_ = store.Upsert("", "Jan Brygadzista", "1111", true)
	_ = store.Upsert("", "Ola Brygadzista", "", true)
	ids := map[string]string{}
	for _, w := range store.Workers() {
		ids[w.Name] = w.ID
	}
	hub := NewHub(store, NewCallLog(store.dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler(hub, store))
	adminRoutes(mux, hub, store)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	key := store.Config.DeviceKey

	anna := dial(t, srv.URL, url.Values{"role": {"terminal"}, "key": {key}, "device": {"T1"}, "ver": {"1.3.0"}})
	anna.send(map[string]any{"t": "login", "worker": ids["Anna Nowak"]})
	anna.wait("login_ok", nil)
	jan := dial(t, srv.URL, url.Values{"role": {"terminal"}, "key": {key}, "device": {"T2"}, "ver": {"1.3.0"}})
	jan.send(map[string]any{"t": "login", "worker": ids["Jan Brygadzista"], "pin": "1111"})
	jan.wait("login_ok", nil)
	jan.send(map[string]any{"t": "status", "battery": 55, "charging": true})

	tab := dial(t, srv.URL, url.Values{"role": {"tablet"}, "key": {key}, "station": {"KONTR_01"}, "device": {"A"}, "ver": {"1.3.0"}})
	tab.wait("state", nil)
	tab.send(map[string]any{"t": "call", "worker": ids["Anna Nowak"]})
	call := anna.wait("call", nil)

	// 61 s bez odpowiedzi
	hub.mu.Lock()
	if hub.expireLocked(time.Now().Add(61 * time.Second)) {
		hub.broadcastStateLocked()
	}
	hub.mu.Unlock()
	a := jan.wait("alert", nil)
	if a["station"] != "KONTR_01" || a["worker"] != "Anna Nowak" || a["id"] != call["id"] {
		t.Fatalf("alert: %v", a)
	}
	tab.wait("state", func(m map[string]any) bool {
		for _, w := range m["workers"].([]any) {
			if wm := w.(map[string]any); wm["name"] == "Anna Nowak" {
				return wm["escalated"] == true
			}
		}
		return false
	})

	// brygadzista logujący się później też dostaje trwający alert
	ola := dial(t, srv.URL, url.Values{"role": {"terminal"}, "key": {key}, "device": {"T3"}})
	ola.send(map[string]any{"t": "login", "worker": ids["Ola Brygadzista"]})
	ola.wait("alert", func(m map[string]any) bool { return m["id"] == call["id"] })

	// panel brygadzisty: tylko z hasłem, tylko odczyt
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var cookie *http.Cookie
	get := func(path, pass string) *http.Response {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if pass != "" {
			req.SetBasicAuth("", pass)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	login := func(id, pin string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/brygadzista/logowanie", strings.NewReader(url.Values{"id": {id}, "pin": {pin}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", srv.URL)
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// bez sesji: przekierowanie na logowanie, dane 401
	if r := get("/brygadzista", ""); r.StatusCode != 303 || r.Header.Get("Location") != "/brygadzista/logowanie" {
		t.Fatalf("brygadzista bez sesji: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := get("/brygadzista/stan", ""); r.StatusCode != 401 {
		t.Fatalf("stan bez sesji: %d", r.StatusCode)
	}
	// strona logowania pokazuje tylko brygadzistów z PIN-em
	lp := get("/brygadzista/logowanie", "")
	body, _ := io.ReadAll(lp.Body)
	if !strings.Contains(string(body), "Jan Brygadzista") || strings.Contains(string(body), "Ola Brygadzista") || strings.Contains(string(body), "Anna Nowak") {
		t.Fatalf("lista na stronie logowania: %s", body)
	}
	// zły PIN, brygadzista bez PIN-u, zwykły pracownik: odmowa
	for _, c := range [][2]string{{ids["Jan Brygadzista"], "9999"}, {ids["Ola Brygadzista"], ""}, {ids["Anna Nowak"], ""}} {
		if r := login(c[0], c[1]); r.StatusCode != 200 || len(r.Cookies()) != 0 {
			t.Fatalf("logowanie %v powinno sie nie udac: %d", c, r.StatusCode)
		}
	}
	pinLimiter.Success("w:" + ids["Jan Brygadzista"])
	adminLimiter.Success("127.0.0.1")
	r := login(ids["Jan Brygadzista"], "1111")
	if r.StatusCode != 303 || len(r.Cookies()) != 1 {
		t.Fatalf("logowanie Jana: %d", r.StatusCode)
	}
	cookie = r.Cookies()[0]
	if r := get("/brygadzista", ""); r.StatusCode != 200 {
		t.Fatalf("brygadzista z sesja: %d", r.StatusCode)
	}
	r = get("/brygadzista/stan", "")

	var st struct {
		Alerts    []alertInfo    `json:"alerts"`
		Terminals []TerminalInfo `json:"terminals"`
		Tablets   []TabletInfo   `json:"tablets"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	if len(st.Alerts) != 1 || st.Alerts[0].Station != "KONTR_01" {
		t.Fatalf("alerty w panelu: %+v", st.Alerts)
	}
	foundJan := false
	for _, tm := range st.Terminals {
		if tm.Worker == "Jan Brygadzista" {
			foundJan = true
			if tm.Battery != 55 || !tm.Charging || tm.Version != "1.3.0" || !tm.Foreman || tm.IP == "" {
				t.Fatalf("terminal Jana: %+v", tm)
			}
		}
	}
	if !foundJan || len(st.Tablets) != 1 || st.Tablets[0].Version != "1.3.0" {
		t.Fatalf("terminale/tablety: %+v %+v", st.Terminals, st.Tablets)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/brygadzista", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", srv.URL)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 405 {
		t.Fatalf("POST brygadzista: %d", resp.StatusCode)
	}
	// sesja nie otwiera panelu administratora
	if r := get("/admin", ""); r.StatusCode != 401 {
		t.Fatalf("admin z sesja brygadzisty: %d", r.StatusCode)
	}
	// zmiana PIN-u kończy sesję; hasło administratora otwiera panel brygadzisty
	_ = store.Upsert(ids["Jan Brygadzista"], "Jan Brygadzista", "2222", true)
	if r := get("/brygadzista/stan", ""); r.StatusCode != 401 {
		t.Fatalf("sesja po zmianie PIN-u: %d", r.StatusCode)
	}
	cookie = nil
	if r := get("/brygadzista/stan", store.Config.AdminPassword); r.StatusCode != 200 {
		t.Fatalf("brygadzista haslem admina: %d", r.StatusCode)
	}

	// brygadzista przyjmuje alert, pracownik potwierdza: alert odwołany u wszystkich
	jan.send(map[string]any{"t": "alert_ack", "id": call["id"]})
	anna.send(map[string]any{"t": "ack", "id": call["id"]})
	jan.wait("alert_cancel", func(m map[string]any) bool { return m["id"] == call["id"] })
	ola.wait("alert_cancel", func(m map[string]any) bool { return m["id"] == call["id"] })
	if len(hub.Alerts()) != 0 {
		t.Fatal("alert nie zniknal")
	}

	// statystyki z dziennika
	from, to := StatsPeriod("dzis", time.Now())
	s := ComputeStats(store.dir, from, to)
	if s.Total.Calls != 1 || s.Total.Acks != 1 || s.Total.Escalated != 1 || len(s.Stations) != 1 || s.Stations[0].Name != "KONTR_01" {
		t.Fatalf("statystyki: %+v", s)
	}
	r = get("/admin/statystyki?okres=7dni", store.Config.AdminPassword)
	if r.StatusCode != 200 {
		t.Fatalf("statystyki admin: %d", r.StatusCode)
	}
}

func TestStatsParse(t *testing.T) {
	dir := t.TempDir()
	loc := time.Local
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, loc)
	csv := "\xef\xbb\xbfczas;zdarzenie;pracownik_id;pracownik;stanowisko;info\n" +
		"2026-11-20 08:00:00;wezwanie;a;Anna;KONTR_02;\n" +
		"2026-11-20 08:00:10;potwierdzenie;a;Anna;KONTR_02;10 s\n" +
		"2026-11-20 09:00:00;wezwanie;a;Anna;KONTR_10;\n" +
		"2026-11-20 09:00:30;potwierdzenie;a;Anna;KONTR_10;30 s\n" +
		"2026-11-20 10:00:00;wezwanie;b;Bartek;KONTR_02;\n" +
		"2026-11-20 10:05:00;wygasle;b;Bartek;KONTR_02;\n" +
		"2026-11-14 10:00:00;wezwanie;b;Bartek;KONTR_02;\n"
	if err := os.WriteFile(filepath.Join(dir, "zdarzenia-2026-11.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	from, to := StatsPeriod("dzis", now)
	s := ComputeStats(dir, from, to)
	if s.Total.Calls != 3 || s.Total.Acks != 2 || s.Total.Expired != 1 || s.Total.AvgSec != 20 || s.Total.MedianSec != 20 || s.Total.MaxSec != 30 {
		t.Fatalf("razem: %+v", s.Total)
	}
	if len(s.Stations) != 2 || s.Stations[0].Name != "KONTR_02" || s.Stations[1].Name != "KONTR_10" {
		t.Fatalf("stoly: %+v", s.Stations)
	}
	from, to = StatsPeriod("7dni", now)
	if s = ComputeStats(dir, from, to); s.Total.Calls != 4 {
		t.Fatalf("7 dni: %+v", s.Total)
	}
}

func TestPinStorage(t *testing.T) {
	dir := t.TempDir()
	// workers.json z wersji 1.3.0: PIN otwartym tekstem
	old := `[{"id":"a1","name":"Anna","pin":"1234","foreman":true},{"id":"b2","name":"Bartek","pin":""}]`
	if err := os.WriteFile(filepath.Join(dir, "workers.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "workers.json"))
	if strings.Contains(string(raw), `"1234"`) || !strings.Contains(string(raw), "pbkdf2-sha256$") {
		t.Fatalf("PIN nie zamieniony na skrot: %s", raw)
	}
	if !store.CheckPin("a1", "1234") || store.CheckPin("a1", "1235") || !store.CheckPin("b2", "") || store.CheckPin("b2", "x") {
		t.Fatal("CheckPin po migracji")
	}
	// hasło z literami i znakami (np. z WMS)
	if err := store.UpsertPin("a1", "Anna", "Wms#2026xY", false, true); err != nil {
		t.Fatal(err)
	}
	if !store.CheckPin("a1", "Wms#2026xY") || store.CheckPin("a1", "1234") || !store.ForemanLogin("a1", "Wms#2026xY") {
		t.Fatal("haslo alfanumeryczne")
	}
	// pusty PIN przy zapisie = bez zmian; "usuń" = bez PIN-u
	_ = store.UpsertPin("a1", "Anna", "", false, true)
	if !store.CheckPin("a1", "Wms#2026xY") {
		t.Fatal("pusty PIN zmienil haslo")
	}
	_ = store.UpsertPin("a1", "Anna", "", true, true)
	if !store.CheckPin("a1", "") || store.ForemanLogin("a1", "") {
		t.Fatal("usuniecie PIN-u")
	}
	if err := store.UpsertPin("a1", "Anna", "ma spacje", false, true); err == nil {
		t.Fatal("PIN ze spacja przyjety")
	}
}
