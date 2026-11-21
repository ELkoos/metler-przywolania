package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

type Worker struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Pin: stary zapis otwartym tekstem (do 1.3.0); przy starcie zamieniany na PinHash.
	Pin string `json:"pin,omitempty"`
	// PinHash: PBKDF2-SHA256 PIN-u/hasła; pusty = bez PIN-u.
	PinHash string `json:"pinHash,omitempty"`
	// Brygadzista dostaje alert, gdy wezwany pracownik nie odpowie w ciągu 60 s.
	Foreman bool `json:"foreman,omitempty"`
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
		// migracja: PIN-y zapisane otwartym tekstem -> skrót
		migrated := false
		for i := range s.workers {
			if s.workers[i].Pin != "" {
				s.workers[i].PinHash = hashPin(s.workers[i].Pin)
				s.workers[i].Pin = ""
				migrated = true
			}
		}
		if migrated {
			if err := s.saveLocked(); err != nil {
				return nil, false, err
			}
		}
	}
	return s, created, nil
}

const pinIter = 50000

func hashPin(pin string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, pin, salt, pinIter, 32)
	return "pbkdf2-sha256$" + strconv.Itoa(pinIter) + "$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(k)
}

func pinMatches(hash, pin string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p[2])
	want, err2 := base64.RawStdEncoding.DecodeString(p[3])
	if err1 != nil || err2 != nil {
		return false
	}
	k, err := pbkdf2.Key(sha256.New, pin, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(k, want) == 1
}

// ValidPin: litery, cyfry i znaki specjalne, bez spacji, do 64 znaków (np. hasło z WMS).
func ValidPin(pin string) error {
	if len([]rune(pin)) > 64 {
		return errors.New("PIN może mieć najwyżej 64 znaki")
	}
	for _, r := range pin {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return errors.New("PIN nie może zawierać spacji ani znaków niedrukowalnych")
		}
	}
	return nil
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
		out = append(out, PublicWorker{ID: w.ID, Name: w.Name, HasPin: w.PinHash != ""})
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

// CheckPin: pracownik bez PIN-u przyjmuje tylko pusty PIN.
func (s *Store) CheckPin(id, pin string) bool {
	s.mu.RLock()
	var hash string
	found := false
	for _, w := range s.workers {
		if w.ID == id {
			hash, found = w.PinHash, true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return false
	}
	if hash == "" {
		return pin == ""
	}
	return pinMatches(hash, pin)
}

// ForemanCandidate to brygadzista z PIN-em (może zalogować się do panelu brygadzisty).
type ForemanCandidate struct {
	ID   string
	Name string
}

// Foremen: brygadziści z ustawionym PIN-em, alfabetycznie.
func (s *Store) Foremen() []ForemanCandidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ForemanCandidate{}
	for _, w := range s.workers {
		if w.Foreman && w.PinHash != "" {
			out = append(out, ForemanCandidate{w.ID, w.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ForemanLogin: brygadzista z PIN-em i zgodny PIN.
func (s *Store) ForemanLogin(id, pin string) bool {
	s.mu.RLock()
	var hash string
	for _, w := range s.workers {
		if w.ID == id && w.Foreman {
			hash = w.PinHash
		}
	}
	s.mu.RUnlock()
	return hash != "" && pinMatches(hash, pin)
}

// foremanSecret: PIN brygadzisty (do podpisu sesji; zmiana PIN-u lub odznaczenie kończy sesję).
func (s *Store) foremanSecret(id string) (name, pin string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.workers {
		if w.ID == id && w.Foreman && w.PinHash != "" {
			return w.Name, w.PinHash, true
		}
	}
	return "", "", false
}

// Upsert: pusty pin przy istniejącym pracowniku = PIN bez zmian (clearPin usuwa PIN).
func (s *Store) Upsert(id, name, pin string, foreman bool) error {
	return s.UpsertPin(id, name, pin, false, foreman)
}

func (s *Store) UpsertPin(id, name, pin string, clearPin, foreman bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Login jest wymagany")
	}
	if err := ValidPin(pin); err != nil {
		return err
	}
	newHash := ""
	if pin != "" {
		newHash = hashPin(pin)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.workers {
		if s.workers[i].ID != id && strings.EqualFold(s.workers[i].Name, name) {
			return errors.New("Pracownik o tym loginie już istnieje")
		}
	}
	if id != "" {
		for i := range s.workers {
			if s.workers[i].ID == id {
				s.workers[i].Name = name
				if clearPin {
					s.workers[i].PinHash = ""
				} else if newHash != "" {
					s.workers[i].PinHash = newHash
				}
				s.workers[i].Foreman = foreman
				return s.saveLocked()
			}
		}
		return errors.New("Nie znaleziono pracownika")
	}
	s.workers = append(s.workers, Worker{ID: randHex(4), Name: name, PinHash: newHash, Foreman: foreman})
	return s.saveLocked()
}

// SetForeman zaznacza/odznacza brygadzistę.
func (s *Store) SetForeman(id string, v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.workers {
		if s.workers[i].ID == id {
			if s.workers[i].Foreman == v {
				return nil
			}
			s.workers[i].Foreman = v
			return s.saveLocked()
		}
	}
	return errors.New("Nie znaleziono pracownika")
}

func (s *Store) IsForeman(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.workers {
		if w.ID == id {
			return w.Foreman
		}
	}
	return false
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
