package main

import (
	"bufio"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Statystyki czasu reakcji liczone z dziennika zdarzeń (zdarzenia-RRRR-MM.csv).

type StatRow struct {
	Name      string  `json:"name"`
	Calls     int     `json:"calls"`     // wezwania
	Acks      int     `json:"acks"`      // potwierdzenia
	Expired   int     `json:"expired"`   // wygasłe bez odpowiedzi
	Cancelled int     `json:"cancelled"` // anulowane z tabletu
	Escalated int     `json:"escalated"` // alerty do brygadzistów
	AvgSec    float64 `json:"avgSec"`
	MedianSec float64 `json:"medianSec"`
	MaxSec    float64 `json:"maxSec"`
	times     []float64
}

type Stats struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Total    StatRow   `json:"total"`
	Workers  []StatRow `json:"workers"`
	Stations []StatRow `json:"stations"`
}

func (r *StatRow) add(event string, secs float64, hasSecs bool) {
	switch event {
	case "wezwanie":
		r.Calls++
	case "potwierdzenie":
		r.Acks++
		if hasSecs {
			r.times = append(r.times, secs)
		}
	case "wygasle":
		r.Expired++
	case "anulowanie":
		r.Cancelled++
	case "eskalacja":
		r.Escalated++
	}
}

func (r *StatRow) finish() {
	if len(r.times) == 0 {
		return
	}
	sort.Float64s(r.times)
	sum := 0.0
	for _, t := range r.times {
		sum += t
	}
	r.AvgSec = sum / float64(len(r.times))
	n := len(r.times)
	if n%2 == 1 {
		r.MedianSec = r.times[n/2]
	} else {
		r.MedianSec = (r.times[n/2-1] + r.times[n/2]) / 2
	}
	r.MaxSec = r.times[n-1]
}

// ComputeStats liczy statystyki zdarzeń z okresu [from, to).
func ComputeStats(dir string, from, to time.Time) Stats {
	st := Stats{From: from, To: to, Total: StatRow{Name: "Razem"}}
	workers := map[string]*StatRow{}
	stations := map[string]*StatRow{}
	// pliki miesięczne od miesiąca "from" do miesiąca "to"
	for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location()); !m.After(to); m = m.AddDate(0, 1, 0) {
		f, err := os.Open(filepath.Join(dir, "zdarzenia-"+m.Format("2006-01")+".csv"))
		if err != nil {
			continue
		}
		br := bufio.NewReader(f)
		if b, _ := br.Peek(3); len(b) == 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
			_, _ = br.Discard(3)
		}
		cr := csv.NewReader(br)
		cr.Comma = ';'
		cr.FieldsPerRecord = -1
		for {
			rec, err := cr.Read()
			if err == io.EOF {
				break
			}
			if err != nil || len(rec) < 6 || rec[0] == "czas" {
				continue
			}
			t, err := time.ParseInLocation("2006-01-02 15:04:05", rec[0], from.Location())
			if err != nil || t.Before(from) || !t.Before(to) {
				continue
			}
			event, name, station := rec[1], rec[3], rec[4]
			secs, hasSecs := 0.0, false
			if event == "potwierdzenie" {
				if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(rec[5], "s")), 64); err == nil {
					secs, hasSecs = v, true
				}
			}
			switch event {
			case "wezwanie", "potwierdzenie", "wygasle", "anulowanie", "eskalacja":
			default:
				continue
			}
			st.Total.add(event, secs, hasSecs)
			if name != "" {
				w := workers[name]
				if w == nil {
					w = &StatRow{Name: name}
					workers[name] = w
				}
				w.add(event, secs, hasSecs)
			}
			if station != "" {
				s := stations[station]
				if s == nil {
					s = &StatRow{Name: station}
					stations[station] = s
				}
				s.add(event, secs, hasSecs)
			}
		}
		f.Close()
	}
	st.Total.finish()
	for _, w := range workers {
		w.finish()
		st.Workers = append(st.Workers, *w)
	}
	for _, s := range stations {
		s.finish()
		st.Stations = append(st.Stations, *s)
	}
	sort.Slice(st.Workers, func(i, j int) bool { return st.Workers[i].Name < st.Workers[j].Name })
	sort.Slice(st.Stations, func(i, j int) bool { return naturalLess(st.Stations[i].Name, st.Stations[j].Name) })
	return st
}

// StatsPeriod: "dzis" = od północy, "7dni" = ostatnie 7 dni łącznie z dzisiaj.
func StatsPeriod(period string, now time.Time) (time.Time, time.Time) {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch period {
	case "7dni":
		return midnight.AddDate(0, 0, -6), now.Add(time.Second)
	case "30dni":
		return midnight.AddDate(0, 0, -29), now.Add(time.Second)
	default:
		return midnight, now.Add(time.Second)
	}
}
