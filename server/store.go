package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Worker struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pin  string `json:"pin"` // pusty = bez PIN-u
}

type Config struct {
	AdminPassword string `json:"adminPassword"`
	DeviceKey     string `json:"deviceKey"`
}

type Store struct {
	mu       sync.RWMutex
	dir      string
	workers  []Worker
	stations map[string]string // ID tabletu -> nazwa stołu (np. KONTR_12)
	Config   Config
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func OpenStore(dir string) (*Store, bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	s := &Store{dir: dir}
	created := false
	cfgPath := filepath.Join(dir, "config.json")
	if b, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(b, &s.Config); err != nil {
			return nil, false, errors.New("config.json: " + err.Error())
		}
		// brakujące pola (np. plik przygotowany ręcznie tylko z kluczem) uzupełniamy
		if s.Config.AdminPassword == "" || s.Config.DeviceKey == "" {
			if s.Config.AdminPassword == "" {
				s.Config.AdminPassword = randHex(6)
			}
			if s.Config.DeviceKey == "" {
				s.Config.DeviceKey = randHex(4)
			}
			if err := writeJSON(cfgPath, s.Config); err != nil {
				return nil, false, err
			}
		}
	} else if os.IsNotExist(err) {
		s.Config = Config{AdminPassword: randHex(6), DeviceKey: randHex(4)}
		if err := writeJSON(cfgPath, s.Config); err != nil {
			return nil, false, err
		}
		created = true
	} else {
		return nil, false, err
	}
	s.stations = map[string]string{}
	if b, err := os.ReadFile(filepath.Join(dir, "stoly.json")); err == nil {
		if err := json.Unmarshal(b, &s.stations); err != nil {
			return nil, false, errors.New("stoly.json: " + err.Error())
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "workers.json")); err == nil {
		if err := json.Unmarshal(b, &s.workers); err != nil {
			return nil, false, errors.New("workers.json: " + err.Error())
		}
	}
	return s, created, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) saveLocked() error {
	return writeJSON(filepath.Join(s.dir, "workers.json"), s.workers)
}

func (s *Store) Workers() []Worker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Worker(nil), s.workers...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type PublicWorker struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	HasPin bool   `json:"hasPin"`
}

func (s *Store) PublicWorkers() []PublicWorker {
	out := []PublicWorker{}
	for _, w := range s.Workers() {
		out = append(out, PublicWorker{ID: w.ID, Name: w.Name, HasPin: w.Pin != ""})
	}
	return out
}

func (s *Store) WorkerName(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.workers {
		if w.ID == id {
			return w.Name
		}
	}
	return "?"
}

func (s *Store) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.workers {
		if w.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) CheckPin(id, pin string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.workers {
		if w.ID == id {
			return subtle.ConstantTimeCompare([]byte(w.Pin), []byte(pin)) == 1
		}
	}
	return false
}

func (s *Store) Upsert(id, name, pin string) error {
	name = strings.TrimSpace(name)
	pin = strings.TrimSpace(pin)
	if name == "" {
		return errors.New("Imię i nazwisko jest wymagane")
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return errors.New("PIN może zawierać tylko cyfry")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.workers {
		if s.workers[i].ID != id && strings.EqualFold(s.workers[i].Name, name) {
			return errors.New("Pracownik o tej nazwie już istnieje")
		}
	}
	if id != "" {
		for i := range s.workers {
			if s.workers[i].ID == id {
				s.workers[i].Name = name
				s.workers[i].Pin = pin
				return s.saveLocked()
			}
		}
		return errors.New("Nie znaleziono pracownika")
	}
	s.workers = append(s.workers, Worker{ID: randHex(4), Name: name, Pin: pin})
	return s.saveLocked()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.workers {
		if s.workers[i].ID == id {
			s.workers = append(s.workers[:i], s.workers[i+1:]...)
			return s.saveLocked()
		}
	}
	return nil
}

// NormalizeStation: wielkie litery, spacje na "_" (np. "kontr 12" -> "KONTR_12").
func NormalizeStation(n string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(n)), " ", "_")
}

// Station zwraca nazwę stołu przypisaną do tabletu (pusta, gdy brak).
func (s *Store) Station(device string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stations[device]
}

// SetStation przypisuje stół do tabletu; pusta nazwa usuwa przypisanie.
func (s *Store) SetStation(device, name string) error {
	if device == "" {
		return errors.New("Brak ID tabletu")
	}
	name = NormalizeStation(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		delete(s.stations, device)
	} else {
		s.stations[device] = name
	}
	return writeJSON(filepath.Join(s.dir, "stoly.json"), s.stations)
}

// Stations zwraca kopię przypisań.
func (s *Store) Stations() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.stations))
	for k, v := range s.stations {
		out[k] = v
	}
	return out
}
