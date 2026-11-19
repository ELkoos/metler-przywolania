package main

import (
	"math"
	"sort"
	"time"
)

type Alert struct {
	Kind     string     `json:"kind"`  // srodki, niezeskanowana
	Level    string     `json:"level"` // czerwony, zolty, info
	Text     string     `json:"text"`
	DaysLeft *int       `json:"daysLeft,omitempty"`
	Deadline *time.Time `json:"deadline,omitempty"`
	Since    time.Time  `json:"since"`
	ScanID   string     `json:"scanId,omitempty"`
	Allegro  *AReturn   `json:"allegro,omitempty"`
	Base     *BReturn   `json:"base,omitempty"`
}

func days(d time.Duration) int { return int(math.Floor(d.Hours() / 24)) }

// Alerts liczy alerty na chwilę "now".
func (s *Store) Alerts(cfg Config, now time.Time) []Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Alert
	firstScan := map[string]*Scan{}
	for _, sc := range s.st.Scans {
		if sc.AllegroID == "" {
			continue
		}
		if f, ok := firstScan[sc.AllegroID]; !ok || sc.At.Before(f.At) {
			firstScan[sc.AllegroID] = sc
		}
	}
	// 1. Paczka przyjęta, a pieniądze niezwrócone
	for aid, sc := range firstScan {
		a := s.st.Allegro[aid]
		if a == nil || closedStatus[a.Status] || (a.Refund != nil && a.Refund.Success) {
			continue
		}
		al := Alert{Kind: "srodki", Level: "info", Since: sc.At, ScanID: sc.ID}
		cp := *a
		al.Allegro = &cp
		if b, ok := s.st.Base[sc.BaseID]; ok {
			cb := *b
			al.Base = &cb
		}
		age := days(now.Sub(sc.At))
		al.Text = "Paczka przyjęta, zwrot środków niewykonany"
		if age >= cfg.RefundAlertDays {
			al.Level = "zolty"
		}
		if cfg.DeadlineDays > 0 {
			start := sc.At
			if cfg.DeadlineFrom == "doreczenie" && !a.FirstSeenDelivered.IsZero() {
				start = a.FirstSeenDelivered
			}
			dl := start.AddDate(0, 0, cfg.DeadlineDays)
			left := days(dl.Sub(now))
			if dl.Before(now) {
				left = -days(now.Sub(dl)) - 1
			}
			al.Deadline, al.DaysLeft = &dl, &left
			if left <= cfg.DeadlineWarnDays {
				al.Level = "czerwony"
			}
		}
		out = append(out, al)
	}
	// 2. Allegro: doręczona, a nikt jej nie zeskanował
	for aid, a := range s.st.Allegro {
		if !deliveredStatus[a.Status] || firstScan[aid] != nil || a.FirstSeenDelivered.IsZero() {
			continue
		}
		if days(now.Sub(a.FirstSeenDelivered)) < cfg.NotScannedDays {
			continue
		}
		cp := *a
		out = append(out, Alert{Kind: "niezeskanowana", Level: "zolty", Since: a.FirstSeenDelivered,
			Text: "Allegro: paczka doręczona, brak skanu", Allegro: &cp})
	}
	rank := map[string]int{"czerwony": 0, "zolty": 1, "info": 2}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Level] != rank[out[j].Level] {
			return rank[out[i].Level] < rank[out[j].Level]
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}
