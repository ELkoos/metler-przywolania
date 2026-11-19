package main

import (
	"context"
	"log"
	"time"
)

// Statusy Allegro, w których proces zwrotu pieniędzy jest zakończony.
var closedStatus = map[string]bool{"FINISHED": true, "FINISHED_APT": true, "REJECTED": true}

// Statusy, w których paczka jest już u nas (Allegro tak twierdzi).
var deliveredStatus = map[string]bool{"DELIVERED": true}

type Syncer struct {
	cfg     *ConfigStore
	store   *Store
	allegro *Allegro
	base    *Base
	now     func() time.Time
	trigger chan struct{}
}

func NewSyncer(cfg *ConfigStore, st *Store, a *Allegro, b *Base) *Syncer {
	return &Syncer{cfg: cfg, store: st, allegro: a, base: b, now: time.Now, trigger: make(chan struct{}, 1)}
}

func (s *Syncer) Kick() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (s *Syncer) Run(ctx context.Context) {
	for {
		s.Once(ctx)
		wait := time.Duration(s.cfg.Get().SyncMinutes) * time.Minute
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.trigger:
		}
	}
}

func (s *Syncer) setErr(key string, err error) {
	s.store.mu.Lock()
	if err != nil {
		s.store.st.SyncErrors[key] = s.now().Format("2006-01-02 15:04") + " " + err.Error()
		log.Printf("synchronizacja %s: %v", key, err)
	} else {
		delete(s.store.st.SyncErrors, key)
	}
	s.store.mu.Unlock()
}

func (s *Syncer) Once(ctx context.Context) {
	cfg := s.cfg.Get()
	now := s.now()
	if cfg.AllegroClientID != "" && len(cfg.AllegroAccounts) > 0 {
		s.store.mu.RLock()
		since := s.store.st.LastAllegroSync.Add(-30 * time.Minute)
		s.store.mu.RUnlock()
		if min := now.AddDate(0, 0, -cfg.HistoryDays); since.Before(min) {
			since = min
		}
		ok := true
		for _, acc := range cfg.AllegroAccounts {
			list, err := s.allegro.Returns(ctx, acc.Name, since)
			s.setErr("allegro "+acc.Name, err)
			if err != nil {
				ok = false
			}
			s.MergeAllegro(list, now)
		}
		s.checkRefunds(ctx, now)
		s.store.mu.Lock()
		if ok {
			s.store.st.LastAllegroSync = now
		}
		s.store.mu.Unlock()
	}
	if cfg.BaseToken != "" {
		s.store.mu.RLock()
		needStatuses := len(s.store.st.BaseStatuses) == 0 || now.Sub(s.store.st.LastBaseSync) > 24*time.Hour
		s.store.mu.RUnlock()
		if needStatuses {
			rs, ps, err := s.base.Statuses(ctx)
			s.setErr("base statusy", err)
			if err == nil {
				s.store.mu.Lock()
				s.store.st.BaseStatuses = rs
				if len(ps) > 0 {
					s.store.st.BaseProdStatus = ps
				}
				s.store.mu.Unlock()
			}
		}
		list, err := s.base.Returns(ctx, now.AddDate(0, 0, -cfg.HistoryDays))
		s.setErr("base", err)
		if err == nil {
			s.store.mu.Lock()
			for _, r := range list {
				s.store.st.Base[r.ReturnID] = r
			}
			s.store.st.LastBaseSync = now
			s.store.mu.Unlock()
		}
	}
	s.store.mu.Lock()
	s.store.pruneLocked(now)
	s.store.rematchLocked()
	_ = s.store.saveLocked()
	s.store.mu.Unlock()
}

// MergeAllegro wstawia/aktualizuje zwroty Allegro, zachowując nasze znaczniki.
func (s *Syncer) MergeAllegro(list []*AReturn, now time.Time) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for _, r := range list {
		if old, ok := s.store.st.Allegro[r.ID]; ok {
			r.FirstSeenDelivered = old.FirstSeenDelivered
			r.Refund = old.Refund
			r.RefundChecked = old.RefundChecked
		}
		if r.FirstSeenDelivered.IsZero() && (deliveredStatus[r.Status] || closedStatus[r.Status]) {
			r.FirstSeenDelivered = now
		}
		s.store.st.Allegro[r.ID] = r
	}
}

// checkRefunds – dla zeskanowanych, niezakończonych zwrotów sprawdza zwroty płatności (raz na godzinę na zamówienie).
func (s *Syncer) checkRefunds(ctx context.Context, now time.Time) {
	type job struct{ id, account, order string }
	var jobs []job
	s.store.mu.RLock()
	scanned := map[string]bool{}
	for _, sc := range s.store.st.Scans {
		if sc.AllegroID != "" {
			scanned[sc.AllegroID] = true
		}
	}
	for id := range scanned {
		a := s.store.st.Allegro[id]
		if a == nil || closedStatus[a.Status] || (a.Refund != nil && a.Refund.Success) {
			continue
		}
		if now.Sub(a.RefundChecked) < time.Hour {
			continue
		}
		jobs = append(jobs, job{id, a.Account, a.OrderID})
	}
	s.store.mu.RUnlock()
	for _, j := range jobs {
		r, err := s.allegro.RefundFor(ctx, j.account, j.order)
		s.setErr("allegro zwroty platnosci", err)
		if err != nil {
			return
		}
		s.store.mu.Lock()
		if a := s.store.st.Allegro[j.id]; a != nil {
			a.Refund = r
			a.RefundChecked = now
		}
		s.store.mu.Unlock()
	}
}

// LookupLive – gdy skan nie pasuje, pytamy Allegro bezpośrednio po numerze przesyłki.
func (s *Syncer) LookupLive(ctx context.Context, code string) {
	cfg := s.cfg.Get()
	if cfg.AllegroClientID == "" {
		return
	}
	for _, acc := range cfg.AllegroAccounts {
		list, err := s.allegro.ReturnsByWaybill(ctx, acc.Name, code)
		if err != nil {
			s.setErr("allegro "+acc.Name, err)
			continue
		}
		if len(list) > 0 {
			s.MergeAllegro(list, s.now())
			return
		}
	}
}

// pruneLocked usuwa stare dane, które nie są powiązane ze skanem.
func (st *Store) pruneLocked(now time.Time) {
	limit := now.AddDate(0, 0, -180)
	keepA, keepB := map[string]bool{}, map[int64]bool{}
	kept := st.st.Scans[:0]
	for _, sc := range st.st.Scans {
		if sc.At.Before(now.AddDate(-1, 0, 0)) {
			continue // skany starsze niż rok
		}
		kept = append(kept, sc)
		keepA[sc.AllegroID] = true
		keepB[sc.BaseID] = true
	}
	st.st.Scans = kept
	for id, a := range st.st.Allegro {
		if a.UpdatedAt.Before(limit) && !keepA[id] {
			delete(st.st.Allegro, id)
		}
	}
	for id, b := range st.st.Base {
		if b.DateAdd.Before(limit) && !keepB[id] {
			delete(st.st.Base, id)
		}
	}
}
