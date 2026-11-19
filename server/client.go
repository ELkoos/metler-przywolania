package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type Client struct {
	Role    string // tablet | terminal
	Station string // nazwa stanowiska (tablet)
	Device  string // identyfikator urządzenia
	Remote  string
	send    chan []byte
}

func (c *Client) Send(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case c.send <- b:
	default:
		// kolejka pełna: klient nie odbiera, połączenie zostanie zamknięte przez ping
	}
}

func wsHandler(h *Hub, store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		role := q.Get("role")
		key := q.Get("key")
		if subtle.ConstantTimeCompare([]byte(key), []byte(store.Config.DeviceKey)) != 1 {
			http.Error(w, "zly klucz urzadzenia", http.StatusUnauthorized)
			return
		}
		if role != "tablet" && role != "terminal" {
			http.Error(w, "zla rola", http.StatusBadRequest)
			return
		}
		station := strings.TrimSpace(q.Get("station"))
		if role == "tablet" && station == "" {
			http.Error(w, "brak nazwy stanowiska", http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c := &Client{Role: role, Station: station, Device: q.Get("device"), Remote: r.RemoteAddr, send: make(chan []byte, 64)}
		log.Printf("polaczono: %s %s %s %s", role, station, c.Device, c.Remote)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		h.Register(c)
		defer func() {
			h.Unregister(c)
			log.Printf("rozlaczono: %s %s %s", role, station, c.Device)
		}()

		// zapis + ping
		go func() {
			ping := time.NewTicker(15 * time.Second)
			defer ping.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case b := <-c.send:
					wctx, wc := context.WithTimeout(ctx, 10*time.Second)
					err := conn.Write(wctx, websocket.MessageText, b)
					wc()
					if err != nil {
						cancel()
						return
					}
				case <-ping.C:
					pctx, pc := context.WithTimeout(ctx, 10*time.Second)
					err := conn.Ping(pctx)
					pc()
					if err != nil {
						cancel()
						_ = conn.CloseNow()
						return
					}
				}
			}
		}()

		conn.SetReadLimit(64 * 1024)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				_ = conn.CloseNow()
				return
			}
			if string(data) == "ping" {
				c.Send(map[string]any{"t": "pong"})
				continue
			}
			h.Handle(c, data)
		}
	}
}
