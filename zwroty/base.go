package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Base API (api.baselinker.com, metoda getOrderReturns – dokumentacja odczytana 19.11.2026).
// Tylko odczyt. Zapytania sekwencyjne, min. 1 s odstępu (limit konta dzielony z Subsync).
var baseURL = "https://api.baselinker.com/connector.php"

type Base struct {
	cfg  *ConfigStore
	http *http.Client
	mu   sync.Mutex
	last time.Time
	gap  time.Duration
}

func NewBase(cfg *ConfigStore) *Base {
	return &Base{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}, gap: time.Second}
}

// flexInt przyjmuje liczbę albo tekst z liczbą.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	*f = flexInt(v)
	return nil
}

// flexStr przyjmuje tekst albo liczbę.
type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var x string
		if err := json.Unmarshal(b, &x); err != nil {
			return err
		}
		*f = flexStr(x)
		return nil
	}
	*f = flexStr(s)
	return nil
}

func (b *Base) call(ctx context.Context, method string, params any, out any) error {
	tok := b.cfg.Get().BaseToken
	if tok == "" {
		return errors.New("brak tokenu Base")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if w := b.gap - time.Since(b.last); w > 0 {
		select {
		case <-time.After(w):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() { b.last = time.Now() }()
	pj, _ := json.Marshal(params)
	form := url.Values{"method": {method}, "parameters": {string(pj)}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, strings.NewReader(form.Encode()))
	req.Header.Set("X-BLToken", tok)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var head struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
		ErrorCode    string `json:"error_code"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("Base %s HTTP %d: %s", method, resp.StatusCode, truncate(string(body), 200))
	}
	if head.Status != "SUCCESS" {
		return fmt.Errorf("Base %s: %s %s", method, head.ErrorCode, head.ErrorMessage)
	}
	return json.Unmarshal(body, out)
}

type apiBaseReturn struct {
	ReturnID        flexInt `json:"return_id"`
	OrderID         flexInt `json:"order_id"`
	ExternalOrderID flexStr `json:"external_order_id"`
	ReferenceNumber flexStr `json:"reference_number"`
	Source          flexStr `json:"order_return_source"`
	StatusID        flexInt `json:"status_id"`
	DateAdd         flexInt `json:"date_add"`
	DateInStatus    flexInt `json:"date_in_status"`
	Fullname        flexStr `json:"delivery_fullname"`
	PackageModule   flexStr `json:"delivery_package_module"`
	PackageNr       flexStr `json:"delivery_package_nr"`
	Products        []struct {
		Name     flexStr `json:"name"`
		SKU      flexStr `json:"sku"`
		EAN      flexStr `json:"ean"`
		Quantity flexInt `json:"quantity"`
		StatusID flexInt `json:"status_id"`
	} `json:"products"`
}

func unix(v flexInt) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(v), 0)
}

// Returns pobiera zwroty utworzone od "since" (max 100 na wywołanie, dalej po id_from).
func (b *Base) Returns(ctx context.Context, since time.Time) ([]*BReturn, error) {
	var out []*BReturn
	params := map[string]any{"date_from": since.Unix()}
	for page := 0; page < 200; page++ {
		var resp struct {
			Returns []apiBaseReturn `json:"returns"`
		}
		if err := b.call(ctx, "getOrderReturns", params, &resp); err != nil {
			return out, err
		}
		var maxID int64
		for _, r := range resp.Returns {
			m := &BReturn{ReturnID: int64(r.ReturnID), OrderID: int64(r.OrderID), ExternalOrderID: string(r.ExternalOrderID),
				ReferenceNumber: string(r.ReferenceNumber), Source: string(r.Source), StatusID: int64(r.StatusID),
				DateAdd: unix(r.DateAdd), DateInStatus: unix(r.DateInStatus), Fullname: string(r.Fullname),
				PackageModule: string(r.PackageModule), PackageNr: string(r.PackageNr)}
			for _, p := range r.Products {
				m.Products = append(m.Products, BProduct{Name: string(p.Name), SKU: string(p.SKU), EAN: string(p.EAN),
					Quantity: int64(p.Quantity), StatusID: int64(p.StatusID)})
			}
			out = append(out, m)
			if m.ReturnID > maxID {
				maxID = m.ReturnID
			}
		}
		if len(resp.Returns) < 100 || maxID == 0 {
			return out, nil
		}
		params = map[string]any{"date_from": since.Unix(), "id_from": maxID + 1}
	}
	return out, nil
}

// Statuses – nazwy statusów zwrotów i statusów produktów (do wyświetlenia).
func (b *Base) Statuses(ctx context.Context) (map[int64]string, map[int64]string, error) {
	type st struct {
		ID   flexInt `json:"id"`
		Name flexStr `json:"name"`
	}
	var r1 struct {
		Statuses []st `json:"statuses"`
	}
	ret := map[int64]string{}
	if err := b.call(ctx, "getOrderReturnStatusList", map[string]any{}, &r1); err != nil {
		return nil, nil, err
	}
	for _, s := range r1.Statuses {
		ret[int64(s.ID)] = string(s.Name)
	}
	prod := map[int64]string{}
	var r2 map[string]json.RawMessage
	if err := b.call(ctx, "getOrderReturnProductStatuses", map[string]any{}, &r2); err == nil {
		for k, raw := range r2 {
			if k == "status" {
				continue
			}
			var list []st
			if json.Unmarshal(raw, &list) == nil {
				for _, s := range list {
					prod[int64(s.ID)] = string(s.Name)
				}
			}
		}
	}
	return ret, prod, nil
}
