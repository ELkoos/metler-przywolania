package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AReturn – zwrot z Allegro (GET /order/customer-returns), tylko potrzebne pola.
type AReturn struct {
	ID              string    `json:"id"`
	Account         string    `json:"account"`
	ReferenceNumber string    `json:"referenceNumber"`
	OrderID         string    `json:"orderId"`
	BuyerLogin      string    `json:"buyerLogin"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	Items           []AItem   `json:"items"`
	Parcels         []AParcel `json:"parcels"`
	Rejection       string    `json:"rejection,omitempty"`
	// nasze znaczniki
	FirstSeenDelivered time.Time `json:"firstSeenDelivered,omitempty"`
	Refund             *Refund   `json:"refund,omitempty"`
	RefundChecked      time.Time `json:"refundChecked,omitempty"`
}

type AItem struct {
	Name     string `json:"name"`
	Quantity int64  `json:"quantity"`
	Reason   string `json:"reason"`
	Comment  string `json:"comment,omitempty"`
}

type AParcel struct {
	Waybill               string `json:"waybill"`
	CarrierID             string `json:"carrierId"`
	TransportingWaybill   string `json:"transportingWaybill,omitempty"`
	TransportingCarrierID string `json:"transportingCarrierId,omitempty"`
}

// Refund – podsumowanie zwrotów płatności Allegro (GET /payments/refunds) dla zamówienia.
type Refund struct {
	Statuses []string  `json:"statuses"`
	Success  bool      `json:"success"` // jest zwrot SUCCESS lub PARTIAL
	Amount   string    `json:"amount"`
	Currency string    `json:"currency"`
	At       time.Time `json:"at"`
}

// BReturn – zwrot z Base (getOrderReturns).
type BReturn struct {
	ReturnID        int64      `json:"returnId"`
	OrderID         int64      `json:"orderId"`
	ReferenceNumber string     `json:"referenceNumber"`
	ExternalOrderID string     `json:"externalOrderId"`
	Source          string     `json:"source"`
	StatusID        int64      `json:"statusId"`
	DateAdd         time.Time  `json:"dateAdd"`
	DateInStatus    time.Time  `json:"dateInStatus"`
	PackageModule   string     `json:"packageModule"`
	PackageNr       string     `json:"packageNr"`
	Fullname        string     `json:"fullname"`
	Products        []BProduct `json:"products"`
}

type BProduct struct {
	Name     string `json:"name"`
	SKU      string `json:"sku"`
	EAN      string `json:"ean"`
	Quantity int64  `json:"quantity"`
	StatusID int64  `json:"statusId"`
}

// Scan – jeden skan paczki.
type Scan struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"` // po normalizacji
	Raw       string    `json:"raw"`
	Batch     string    `json:"batch"` // partia / przewoźnik wybrany przez pracownika
	Worker    string    `json:"worker"`
	At        time.Time `json:"at"`
	AllegroID string    `json:"allegroId,omitempty"`
	BaseID    int64     `json:"baseId,omitempty"`
	Match     string    `json:"match,omitempty"` // allegro-waybill, allegro-transport, base-package, reczne
	Resolved  bool      `json:"resolved,omitempty"`
	Note      string    `json:"note,omitempty"`
	Duplicate bool      `json:"duplicate,omitempty"`
}

func (s Scan) Matched() bool { return s.AllegroID != "" || s.BaseID != 0 }

type State struct {
	Allegro         map[string]*AReturn `json:"allegro"`
	Base            map[int64]*BReturn  `json:"base"`
	Scans           []*Scan             `json:"scans"`
	BaseStatuses    map[int64]string    `json:"baseStatuses"`
	BaseProdStatus  map[int64]string    `json:"baseProdStatus"`
	LastAllegroSync time.Time           `json:"lastAllegroSync"`
	LastBaseSync    time.Time           `json:"lastBaseSync"`
	BaseLastID      int64               `json:"baseLastId"`
	SyncErrors      map[string]string   `json:"syncErrors"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	st   State
}

func OpenStore(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "stan.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		if err := json.Unmarshal(b, &s.st); err != nil {
			return nil, errors.New("stan.json: " + err.Error())
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.st.Allegro == nil {
		s.st.Allegro = map[string]*AReturn{}
	}
	if s.st.Base == nil {
		s.st.Base = map[int64]*BReturn{}
	}
	if s.st.BaseStatuses == nil {
		s.st.BaseStatuses = map[int64]string{}
	}
	if s.st.BaseProdStatus == nil {
		s.st.BaseProdStatus = map[int64]string{}
	}
	if s.st.SyncErrors == nil {
		s.st.SyncErrors = map[string]string{}
	}
	return s, nil
}

func (s *Store) saveLocked() error { return writeJSON(s.path, s.st) }

// NormCode – wielkie litery, tylko litery i cyfry.
func NormCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchLocked szuka zwrotu do numeru paczki. Najpierw dokładnie, potem
// dopasowanie częściowe (jeden numer zawiera drugi, min. 10 znaków) tylko gdy jest dokładnie jeden kandydat.
func (s *Store) matchLocked(code string) (allegroID string, baseID int64, how string) {
	if code == "" {
		return
	}
	type cand struct {
		aid string
		bid int64
		how string
	}
	var exact, partial []cand
	add := func(list *[]cand, c cand) {
		for _, x := range *list {
			if x.aid == c.aid && x.bid == c.bid {
				return
			}
		}
		*list = append(*list, c)
	}
	contains := func(a, b string) bool {
		return len(a) >= 10 && len(b) >= 10 && (strings.Contains(a, b) || strings.Contains(b, a))
	}
	for id, r := range s.st.Allegro {
		for _, p := range r.Parcels {
			w, t := NormCode(p.Waybill), NormCode(p.TransportingWaybill)
			switch {
			case w != "" && w == code:
				add(&exact, cand{aid: id, how: "allegro-list"})
			case t != "" && t == code:
				add(&exact, cand{aid: id, how: "allegro-przewoznik"})
			case contains(w, code) || contains(t, code):
				add(&partial, cand{aid: id, how: "allegro-czesciowe"})
			}
		}
	}
	for id, r := range s.st.Base {
		n := NormCode(r.PackageNr)
		switch {
		case n != "" && n == code:
			add(&exact, cand{bid: id, how: "base-list"})
		case contains(n, code):
			add(&partial, cand{bid: id, how: "base-czesciowe"})
		}
	}
	pick := func(list []cand) (string, int64, string, bool) {
		var aids []string
		var bids []int64
		h := ""
		for _, c := range list {
			if c.aid != "" {
				aids = append(aids, c.aid)
			}
			if c.bid != 0 {
				bids = append(bids, c.bid)
			}
			if h == "" || strings.HasPrefix(c.how, "allegro") {
				h = c.how
			}
		}
		if len(aids) > 1 || len(bids) > 1 || len(list) == 0 {
			return "", 0, "", false
		}
		a, b := "", int64(0)
		if len(aids) == 1 {
			a = aids[0]
		}
		if len(bids) == 1 {
			b = bids[0]
		}
		return a, b, h, true
	}
	if a, b, h, ok := pick(exact); ok {
		allegroID, baseID, how = a, b, h
	} else if len(exact) == 0 {
		if a, b, h, ok := pick(partial); ok {
			allegroID, baseID, how = a, b, h
		}
	}
	// uzupełnij brakującą stronę przez powiązanie zamówień
	if allegroID != "" && baseID == 0 {
		baseID = s.baseForAllegroLocked(s.st.Allegro[allegroID])
	}
	if baseID != 0 && allegroID == "" {
		allegroID = s.allegroForBaseLocked(s.st.Base[baseID])
	}
	return
}

// Base external_order_id = ID zamówienia Allegro (sprawdzone na 100 zwrotach 19.11.2026).
func (s *Store) baseForAllegroLocked(a *AReturn) int64 {
	if a == nil {
		return 0
	}
	var found int64
	n := 0
	for id, b := range s.st.Base {
		if b.ExternalOrderID != "" && (b.ExternalOrderID == a.OrderID || b.ExternalOrderID == a.ID) {
			found = id
			n++
		}
	}
	if n == 1 {
		return found
	}
	return 0
}

func (s *Store) allegroForBaseLocked(b *BReturn) string {
	if b == nil || b.ExternalOrderID == "" {
		return ""
	}
	found := ""
	n := 0
	for id, a := range s.st.Allegro {
		if a.OrderID == b.ExternalOrderID || a.ID == b.ExternalOrderID {
			found = id
			n++
		}
	}
	if n == 1 {
		return found
	}
	return ""
}

// AddScan zapisuje skan i od razu próbuje dopasować.
func (s *Store) AddScan(raw, batch, worker string, now time.Time) (*Scan, error) {
	code := NormCode(raw)
	if code == "" {
		return nil, errors.New("pusty kod")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := &Scan{ID: randHex(6), Code: code, Raw: strings.TrimSpace(raw), Batch: batch, Worker: worker, At: now}
	for _, o := range s.st.Scans {
		if o.Code == code {
			sc.Duplicate = true
			break
		}
	}
	sc.AllegroID, sc.BaseID, sc.Match = s.matchLocked(code)
	s.st.Scans = append(s.st.Scans, sc)
	cp := *sc
	return &cp, s.saveLocked()
}

// Rematch – po synchronizacji próbujemy dopasować skany bez zwrotu.
func (s *Store) rematchLocked() {
	for _, sc := range s.st.Scans {
		if sc.Match == "reczne" {
			continue
		}
		if !sc.Matched() {
			sc.AllegroID, sc.BaseID, sc.Match = s.matchLocked(sc.Code)
			continue
		}
		if sc.AllegroID != "" && sc.BaseID == 0 {
			sc.BaseID = s.baseForAllegroLocked(s.st.Allegro[sc.AllegroID])
		}
		if sc.BaseID != 0 && sc.AllegroID == "" {
			sc.AllegroID = s.allegroForBaseLocked(s.st.Base[sc.BaseID])
		}
	}
}

// ResolveScan – ręczne wyjaśnienie paczki: przypisanie numeru zwrotu (RMA Allegro lub Base) albo notatka.
func (s *Store) ResolveScan(id, ref, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sc *Scan
	for _, x := range s.st.Scans {
		if x.ID == id {
			sc = x
		}
	}
	if sc == nil {
		return errors.New("nie ma takiego skanu")
	}
	ref = strings.TrimSpace(ref)
	if ref != "" {
		aid, bid := "", int64(0)
		for id, a := range s.st.Allegro {
			if strings.EqualFold(a.ReferenceNumber, ref) || a.OrderID == ref || a.ID == ref {
				aid = id
			}
		}
		for id, b := range s.st.Base {
			if strings.EqualFold(b.ReferenceNumber, ref) || b.ExternalOrderID == ref {
				bid = id
			}
		}
		if aid == "" && bid == 0 {
			return errors.New("nie znaleziono zwrotu o numerze " + ref)
		}
		if aid != "" && bid == 0 {
			bid = s.baseForAllegroLocked(s.st.Allegro[aid])
		}
		if bid != 0 && aid == "" {
			aid = s.allegroForBaseLocked(s.st.Base[bid])
		}
		sc.AllegroID, sc.BaseID, sc.Match = aid, bid, "reczne"
	}
	sc.Note = strings.TrimSpace(note)
	sc.Resolved = true
	return s.saveLocked()
}

func (s *Store) DeleteScan(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.st.Scans {
		if x.ID == id {
			s.st.Scans = append(s.st.Scans[:i], s.st.Scans[i+1:]...)
			return s.saveLocked()
		}
	}
	return errors.New("nie ma takiego skanu")
}

// ScanView – skan z danymi zwrotu do wyświetlenia.
type ScanView struct {
	Scan
	Allegro    *AReturn `json:"allegro,omitempty"`
	Base       *BReturn `json:"base,omitempty"`
	BaseStatus string   `json:"baseStatus,omitempty"`
}

func (s *Store) viewLocked(sc *Scan) ScanView {
	v := ScanView{Scan: *sc}
	if a, ok := s.st.Allegro[sc.AllegroID]; ok {
		cp := *a
		v.Allegro = &cp
	}
	if b, ok := s.st.Base[sc.BaseID]; ok {
		cp := *b
		v.Base = &cp
		v.BaseStatus = s.st.BaseStatuses[b.StatusID]
	}
	return v
}

func (s *Store) ScanView(id string) (ScanView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.st.Scans {
		if x.ID == id {
			return s.viewLocked(x), true
		}
	}
	return ScanView{}, false
}

// Scans zwraca skany od najnowszych; since – opcjonalny filtr.
func (s *Store) Scans(since time.Time, onlyUnmatched bool) []ScanView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ScanView{}
	for i := len(s.st.Scans) - 1; i >= 0; i-- {
		sc := s.st.Scans[i]
		if sc.At.Before(since) {
			continue
		}
		if onlyUnmatched && (sc.Matched() || sc.Resolved) {
			continue
		}
		out = append(out, s.viewLocked(sc))
	}
	return out
}

func sortedKeys[K comparable, V any](m map[K]V, less func(a, b K) bool) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	return keys
}
