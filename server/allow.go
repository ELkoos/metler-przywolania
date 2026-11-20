package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AllowList przepuszcza tylko żądania z podanych podsieci (np. 192.168.0.0/23) oraz z localhost.
// Pusta lista = brak ograniczenia.
type AllowList struct {
	nets   []*net.IPNet
	mu     sync.Mutex
	logged map[string]time.Time
}

func ParseAllowList(spec string) (*AllowList, error) {
	a := &AllowList{logged: map[string]time.Time{}}
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if !strings.Contains(part, "/") {
			if ip := net.ParseIP(part); ip != nil && ip.To4() != nil {
				part += "/32"
			} else {
				part += "/128"
			}
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("niepoprawna podsieć %q: %v", part, err)
		}
		a.nets = append(a.nets, n)
	}
	return a, nil
}

func (a *AllowList) Allowed(remote string) bool {
	if a == nil || len(a.nets) == 0 {
		return true
	}
	ip := net.ParseIP(hostOf(remote))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, n := range a.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *AllowList) String() string {
	if a == nil || len(a.nets) == 0 {
		return "bez ograniczeń"
	}
	s := make([]string, len(a.nets))
	for i, n := range a.nets {
		s[i] = n.String()
	}
	return strings.Join(s, ", ")
}

// Wrap odrzuca (403) żądania spoza listy; każdy odrzucony adres trafia do dziennika najwyżej raz na godzinę.
func (a *AllowList) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Allowed(r.RemoteAddr) {
			ip := hostOf(r.RemoteAddr)
			a.mu.Lock()
			if time.Since(a.logged[ip]) > time.Hour {
				a.logged[ip] = time.Now()
				if len(a.logged) > 10000 {
					a.logged = map[string]time.Time{ip: time.Now()}
				}
				log.Printf("odrzucono połączenie spoza dozwolonej sieci: %s", ip)
			}
			a.mu.Unlock()
			http.Error(w, "Brak dostępu z tej sieci", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
