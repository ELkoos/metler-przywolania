package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if call["station"] != "Stanowisko 1" {
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
