package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Czasy działania wezwań.
var (
	ackTimeout     = 60 * time.Second // po tym czasie bez potwierdzenia: status "brak odpowiedzi"
	callExpire     = 5 * time.Minute  // po tym czasie niepotwierdzone wezwanie wygasa
	comingDuration = 3 * time.Minute  // jak długo po potwierdzeniu tablet pokazuje "idzie"
	offlineGrace   = 10 * time.Minute // jak długo po utracie połączenia pracownik zostaje na liście (szary)
)

type Call struct {
	ID      string    `json:"id"`
	Station string    `json:"station"`
	At      time.Time `json:"at"`
	AckAt   time.Time `json:"ackAt,omitempty"`
}

// Session to stan pracownika zalogowanego na terminalu.
type Session struct {
	WorkerID     string
	TerminalID   string
	Client       *Client // nil, gdy terminal nie ma połączenia
	LoginAt      time.Time
	DisconnectAt time.Time
	Call         *Call
}

type Hub struct {
	mu       sync.Mutex
	store    *Store
	clients  map[*Client]bool
	sessions map[string]*Session // klucz: workerID
	calls    *CallLog
}

func NewHub(store *Store, calls *CallLog) *Hub {
	h := &Hub{store: store, clients: map[*Client]bool{}, sessions: map[string]*Session{}, calls: calls}
	go h.ticker()
	return h
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Hub) ticker() {
	t := time.NewTicker(5 * time.Second)
	for range t.C {
		h.mu.Lock()
		changed := h.expireLocked(time.Now())
		if changed {
			h.broadcastStateLocked()
		}
		h.mu.Unlock()
	}
}

func (h *Hub) expireLocked(now time.Time) bool {
	changed := false
	for id, s := range h.sessions {
		if s.Client == nil && !s.DisconnectAt.IsZero() && now.Sub(s.DisconnectAt) > offlineGrace {
			h.calls.Write("auto_wylogowanie", id, h.store.WorkerName(id), "", "brak połączenia")
			delete(h.sessions, id)
			changed = true
			continue
		}
		if c := s.Call; c != nil {
			if c.AckAt.IsZero() {
				if now.Sub(c.At) > callExpire {
					h.calls.Write("wygasle", id, h.store.WorkerName(id), c.Station, "")
					h.sendTo(s.Client, map[string]any{"t": "call_cancel", "id": c.ID})
					s.Call = nil
					changed = true
				} else if now.Sub(c.At) > ackTimeout && now.Sub(c.At) <= ackTimeout+5*time.Second {
					changed = true // przejście w "brak odpowiedzi"
				}
			} else if now.Sub(c.AckAt) > comingDuration {
				s.Call = nil
				changed = true
			}
		}
	}
	return changed
}

func (h *Hub) sendTo(c *Client, msg any) {
	if c == nil {
		return
	}
	c.Send(msg)
}

// ---- stan dla tabletów ----

type WorkerState struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"` // dostepny | brak_polaczenia | wezwany | brak_odpowiedzi | idzie
	Station string `json:"station,omitempty"`
	Since   int64  `json:"since,omitempty"` // unix ms
	Online  bool   `json:"online"`          // terminal ma połączenie
}

func (h *Hub) stateLocked() []WorkerState {
	now := time.Now()
	out := []WorkerState{}
	for id, s := range h.sessions {
		ws := WorkerState{ID: id, Name: h.store.WorkerName(id), Status: "dostepny", Online: s.Client != nil}
		if s.Client == nil {
			ws.Status = "brak_polaczenia"
			ws.Since = s.DisconnectAt.UnixMilli()
		}
		if c := s.Call; c != nil {
			ws.Station = c.Station
			if c.AckAt.IsZero() {
				ws.Status = "wezwany"
				if now.Sub(c.At) > ackTimeout {
					ws.Status = "brak_odpowiedzi"
				}
				ws.Since = c.At.UnixMilli()
			} else {
				ws.Status = "idzie"
				ws.Since = c.AckAt.UnixMilli()
			}
		}
		out = append(out, ws)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (h *Hub) broadcastStateLocked() {
	msg := map[string]any{"t": "state", "workers": h.stateLocked(), "now": time.Now().UnixMilli()}
	for c := range h.clients {
		if c.Role == "tablet" {
			c.Send(msg)
		}
	}
}

func (h *Hub) State() []WorkerState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stateLocked()
}

// ---- obsługa klientów ----

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
	if c.Role == "tablet" {
		c.Send(map[string]any{"t": "station", "name": c.Station, "device": c.Device})
		c.Send(map[string]any{"t": "state", "workers": h.stateLocked(), "now": time.Now().UnixMilli()})
	} else {
		c.Send(map[string]any{"t": "workers", "list": h.store.PublicWorkers()})
	}
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	for _, s := range h.sessions {
		if s.Client == c {
			s.Client = nil
			s.DisconnectAt = time.Now()
		}
	}
	h.broadcastStateLocked()
}

// WorkersChanged wysyła terminalom nową listę pracowników i usuwa sesje usuniętych osób.
func (h *Hub) WorkersChanged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.sessions {
		if !h.store.Exists(id) {
			h.sendTo(s.Client, map[string]any{"t": "logged_out", "reason": "Pracownik usunięty z listy"})
			delete(h.sessions, id)
		}
	}
	list := h.store.PublicWorkers()
	for c := range h.clients {
		if c.Role == "terminal" {
			c.Send(map[string]any{"t": "workers", "list": list})
		}
	}
	h.broadcastStateLocked()
}

type inMsg struct {
	T      string `json:"t"`
	Worker string `json:"worker"`
	Pin    string `json:"pin"`
	ID     string `json:"id"`
}

func (h *Hub) Handle(c *Client, raw []byte) {
	var m inMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch c.Role {
	case "terminal":
		h.handleTerminal(c, m)
	case "tablet":
		h.handleTablet(c, m)
	}
}

func (h *Hub) sessionOf(c *Client) (string, *Session) {
	for id, s := range h.sessions {
		if s.Client == c {
			return id, s
		}
	}
	return "", nil
}

func (h *Hub) handleTerminal(c *Client, m inMsg) {
	switch m.T {
	case "login":
		if !h.store.CheckPin(m.Worker, m.Pin) {
			c.Send(map[string]any{"t": "login_err", "reason": "Nieprawidłowy pracownik lub PIN"})
			return
		}
		// ten terminal mógł mieć zalogowaną inną osobę
		if oldID, old := h.sessionOf(c); old != nil && oldID != m.Worker {
			delete(h.sessions, oldID)
		}
		s := h.sessions[m.Worker]
		if s != nil && s.Client != nil && s.Client != c {
			// osoba zalogowana na innym terminalu: przenosimy na nowy
			s.Client.Send(map[string]any{"t": "logged_out", "reason": "Zalogowano na innym terminalu"})
		}
		if s == nil {
			s = &Session{WorkerID: m.Worker, LoginAt: time.Now()}
			h.sessions[m.Worker] = s
			h.calls.Write("logowanie", m.Worker, h.store.WorkerName(m.Worker), "", c.Device)
		}
		s.Client = c
		s.TerminalID = c.Device
		s.DisconnectAt = time.Time{}
		c.Send(map[string]any{"t": "login_ok", "worker": map[string]string{"id": m.Worker, "name": h.store.WorkerName(m.Worker)}})
		if s.Call != nil && s.Call.AckAt.IsZero() {
			c.Send(map[string]any{"t": "call", "id": s.Call.ID, "station": s.Call.Station, "at": s.Call.At.UnixMilli()})
		}
		h.broadcastStateLocked()
	case "logout":
		if id, s := h.sessionOf(c); s != nil {
			h.calls.Write("wylogowanie", id, h.store.WorkerName(id), "", c.Device)
			delete(h.sessions, id)
			c.Send(map[string]any{"t": "logged_out", "reason": ""})
			h.broadcastStateLocked()
		}
	case "ack":
		if id, s := h.sessionOf(c); s != nil && s.Call != nil && s.Call.ID == m.ID && s.Call.AckAt.IsZero() {
			s.Call.AckAt = time.Now()
			secs := s.Call.AckAt.Sub(s.Call.At).Seconds()
			h.calls.Write("potwierdzenie", id, h.store.WorkerName(id), s.Call.Station, fmt.Sprintf("%.0f s", secs))
			h.broadcastStateLocked()
		}
	}
}

func (h *Hub) handleTablet(c *Client, m inMsg) {
	switch m.T {
	case "call":
		if c.Station == "" {
			c.Send(map[string]any{"t": "call_result", "ok": false, "worker": m.Worker, "reason": "Tablet nie ma przypisanego stołu (panel serwera)"})
			return
		}
		s := h.sessions[m.Worker]
		if s == nil {
			c.Send(map[string]any{"t": "call_result", "ok": false, "worker": m.Worker, "reason": "Pracownik nie jest zalogowany"})
			return
		}
		if s.Call != nil && s.Call.AckAt.IsZero() && s.Call.Station != c.Station {
			c.Send(map[string]any{"t": "call_result", "ok": false, "worker": m.Worker, "reason": "Już wezwany przez: " + s.Call.Station})
			return
		}
		// ponowne dotknięcie w ciągu 5 s (np. podwójne kliknięcie) nie tworzy nowego wezwania
		if s.Call != nil && s.Call.AckAt.IsZero() && s.Call.Station == c.Station && time.Since(s.Call.At) < 5*time.Second {
			c.Send(map[string]any{"t": "call_result", "ok": true, "worker": m.Worker, "warning": ""})
			return
		}
		s.Call = &Call{ID: newID(), Station: c.Station, At: time.Now()}
		h.calls.Write("wezwanie", m.Worker, h.store.WorkerName(m.Worker), c.Station, "")
		h.sendTo(s.Client, map[string]any{"t": "call", "id": s.Call.ID, "station": s.Call.Station, "at": s.Call.At.UnixMilli()})
		ok := s.Client != nil
		reason := ""
		if !ok {
			reason = "Terminal bez połączenia – wezwanie dotrze po połączeniu"
		}
		c.Send(map[string]any{"t": "call_result", "ok": true, "worker": m.Worker, "warning": reason})
		h.broadcastStateLocked()
	case "cancel":
		s := h.sessions[m.Worker]
		if s != nil && s.Call != nil && s.Call.Station == c.Station {
			h.calls.Write("anulowanie", m.Worker, h.store.WorkerName(m.Worker), c.Station, "")
			h.sendTo(s.Client, map[string]any{"t": "call_cancel", "id": s.Call.ID})
			s.Call = nil
			h.broadcastStateLocked()
		}
	}
}

func init() { log.SetFlags(log.LstdFlags) }

// StationChanged ustawia nową nazwę stołu połączonym tabletom o danym ID.
func (h *Hub) StationChanged(device, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.Role == "tablet" && c.Device == device {
			c.Station = name
			c.Send(map[string]any{"t": "station", "name": name, "device": device})
		}
	}
}

type TabletInfo struct {
	Device  string `json:"device"`
	Station string `json:"station"`
	IP      string `json:"ip"`
	Online  bool   `json:"online"`
}

// Tablets: wszystkie znane tablety (przypisane i połączone).
func (h *Hub) Tablets() []TabletInfo {
	assigned := h.store.Stations()
	h.mu.Lock()
	defer h.mu.Unlock()
	byDev := map[string]*TabletInfo{}
	for d, n := range assigned {
		byDev[d] = &TabletInfo{Device: d, Station: n}
	}
	for c := range h.clients {
		if c.Role != "tablet" {
			continue
		}
		t := byDev[c.Device]
		if t == nil {
			t = &TabletInfo{Device: c.Device}
			byDev[c.Device] = t
		}
		t.Online = true
		t.IP = c.Remote
		if host, _, err := net.SplitHostPort(c.Remote); err == nil {
			t.IP = host
		}
		t.Station = c.Station
	}
	out := make([]TabletInfo, 0, len(byDev))
	for _, t := range byDev {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Station != out[j].Station {
			return naturalLess(out[i].Station, out[j].Station)
		}
		return out[i].Device < out[j].Device
	})
	return out
}

// naturalLess sortuje KONTR_2 przed KONTR_10; puste nazwy na końcu.
func naturalLess(a, b string) bool {
	if a == "" || b == "" {
		return b == "" && a != ""
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		if ca >= '0' && ca <= '9' && cb >= '0' && cb <= '9' {
			si := i
			for i < len(a) && a[i] >= '0' && a[i] <= '9' {
				i++
			}
			sj := j
			for j < len(b) && b[j] >= '0' && b[j] <= '9' {
				j++
			}
			na, nb := strings.TrimLeft(a[si:i], "0"), strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}
