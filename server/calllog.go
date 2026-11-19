package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CallLog zapisuje zdarzenia do pliku CSV (jeden plik na miesiąc), np. zdarzenia-2026-11.csv.
type CallLog struct {
	mu  sync.Mutex
	dir string
}

func NewCallLog(dir string) *CallLog { return &CallLog{dir: dir} }

func (l *CallLog) Write(event, workerID, workerName, station, info string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	path := filepath.Join(l.dir, "zdarzenia-"+now.Format("2006-01")+".csv")
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Comma = ';'
	if os.IsNotExist(statErr) {
		_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM dla Excela
		_ = w.Write([]string{"czas", "zdarzenie", "pracownik_id", "pracownik", "stanowisko", "info"})
	}
	_ = w.Write([]string{now.Format("2006-01-02 15:04:05"), event, workerID, workerName, station, info})
	w.Flush()
}
