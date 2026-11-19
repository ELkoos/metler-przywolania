package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// AllegroAccount to jedno konto sprzedawcy Allegro autoryzowane w aplikacji.
type AllegroAccount struct {
	Name         string `json:"name"`
	RefreshToken string `json:"refreshToken"`
	AccessToken  string `json:"accessToken,omitempty"`
	Expiry       int64  `json:"expiry,omitempty"` // unix, ważność access tokenu
}

type Config struct {
	AdminPassword string `json:"adminPassword"`

	AllegroClientID     string           `json:"allegroClientId"`
	AllegroClientSecret string           `json:"allegroClientSecret"`
	AllegroAccounts     []AllegroAccount `json:"allegroAccounts"`

	BaseToken string `json:"baseToken"` // token Base API (wystarczy tylko do odczytu)

	SyncMinutes int `json:"syncMinutes"` // co ile minut synchronizacja (domyślnie 15)
	HistoryDays int `json:"historyDays"` // ile dni wstecz pobierać zwroty (domyślnie 60)

	// Alerty
	RefundAlertDays  int      `json:"refundAlertDays"`  // po ilu dniach od skanu alert "brak zwrotu środków" (domyślnie 2)
	DeadlineDays     int      `json:"deadlineDays"`     // termin zwrotu automatycznego w dniach; 0 = nie liczymy
	DeadlineFrom     string   `json:"deadlineFrom"`     // "skan" albo "doreczenie" (pierwszy raz widziany status DELIVERED)
	DeadlineWarnDays int      `json:"deadlineWarnDays"` // ile dni przed terminem alert czerwony (domyślnie 2)
	NotScannedDays   int      `json:"notScannedDays"`   // alert "doręczona, niezeskanowana" po tylu dniach (domyślnie 2)
	Workers          []string `json:"workers"`          // lista osób do wyboru na stanowisku
}

func (c *Config) applyDefaults() {
	if c.SyncMinutes <= 0 {
		c.SyncMinutes = 15
	}
	if c.HistoryDays <= 0 {
		c.HistoryDays = 60
	}
	if c.RefundAlertDays < 0 {
		c.RefundAlertDays = 2
	}
	if c.DeadlineFrom != "doreczenie" {
		c.DeadlineFrom = "skan"
	}
	if c.DeadlineWarnDays < 0 {
		c.DeadlineWarnDays = 2
	}
	if c.NotScannedDays < 0 {
		c.NotScannedDays = 2
	}
}

type ConfigStore struct {
	mu   sync.Mutex
	path string
	c    Config
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func OpenConfig(dir string) (*ConfigStore, bool, error) {
	cs := &ConfigStore{path: filepath.Join(dir, "config.json")}
	created := false
	b, err := os.ReadFile(cs.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &cs.c); err != nil {
			return nil, false, errors.New("config.json: " + err.Error())
		}
	case os.IsNotExist(err):
		created = true
		cs.c.RefundAlertDays, cs.c.DeadlineWarnDays, cs.c.NotScannedDays = 2, 2, 2
	default:
		return nil, false, err
	}
	if cs.c.AdminPassword == "" {
		cs.c.AdminPassword = randHex(6)
		created = true
	}
	cs.c.applyDefaults()
	if err := writeJSON(cs.path, cs.c); err != nil {
		return nil, false, err
	}
	return cs, created, nil
}

func (cs *ConfigStore) Get() Config {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c := cs.c
	c.AllegroAccounts = append([]AllegroAccount(nil), cs.c.AllegroAccounts...)
	c.Workers = append([]string(nil), cs.c.Workers...)
	return c
}

func (cs *ConfigStore) Update(f func(c *Config)) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	f(&cs.c)
	cs.c.applyDefaults()
	return writeJSON(cs.path, cs.c)
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
