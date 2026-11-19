package main

import (
	"context"
	"encoding/base64"
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

// Adresy Allegro (swagger developer.allegro.pl, samouczek uwierzytelniania – odczyt 19.11.2026).
var (
	allegroAuthBase = "https://allegro.pl/auth/oauth"
	allegroAPIBase  = "https://api.allegro.pl"
)

const (
	mediaBeta   = "application/vnd.allegro.beta.v1+json"   // /order/customer-returns
	mediaPublic = "application/vnd.allegro.public.v1+json" // /payments/refunds
)

type Allegro struct {
	cfg  *ConfigStore
	http *http.Client
	mu   sync.Mutex // jedna operacja odświeżenia tokenu naraz
}

func NewAllegro(cfg *ConfigStore) *Allegro {
	return &Allegro{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Allegro) basic() string {
	c := a.cfg.Get()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.AllegroClientID+":"+c.AllegroClientSecret))
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func (a *Allegro) postForm(ctx context.Context, u string, form url.Values, out any) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", a.basic())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(b, out); err != nil {
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return resp.StatusCode, nil
}

// ---- Device flow: dodanie konta Allegro ----

type DeviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

type pendingDevice struct {
	DeviceStart
	Name    string
	Started time.Time
	State   string // czeka, ok, blad
	Msg     string
}

var (
	devMu      sync.Mutex
	devPending = map[string]*pendingDevice{}
)

// StartDevice rozpoczyna autoryzację konta. Zwraca link, który trzeba otworzyć zalogowanym na to konto.
func (a *Allegro) StartDevice(ctx context.Context, name string) (*pendingDevice, error) {
	c := a.cfg.Get()
	if c.AllegroClientID == "" || c.AllegroClientSecret == "" {
		return nil, errors.New("najpierw wpisz Client ID i Client Secret aplikacji Allegro")
	}
	var ds DeviceStart
	code, err := a.postForm(ctx, allegroAuthBase+"/device", url.Values{"client_id": {c.AllegroClientID}}, &ds)
	if err != nil {
		return nil, err
	}
	if code != 200 || ds.DeviceCode == "" {
		return nil, fmt.Errorf("Allegro odrzuciło żądanie (HTTP %d)", code)
	}
	if ds.Interval <= 0 {
		ds.Interval = 5
	}
	if ds.ExpiresIn <= 0 {
		ds.ExpiresIn = 3600
	}
	p := &pendingDevice{DeviceStart: ds, Name: name, Started: time.Now(), State: "czeka"}
	devMu.Lock()
	devPending[ds.UserCode] = p
	devMu.Unlock()
	go a.pollDevice(p)
	return p, nil
}

func (a *Allegro) pollDevice(p *pendingDevice) {
	interval := time.Duration(p.Interval) * time.Second
	deadline := p.Started.Add(time.Duration(p.ExpiresIn) * time.Second)
	set := func(state, msg string) {
		devMu.Lock()
		p.State, p.Msg = state, msg
		devMu.Unlock()
	}
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		var tr tokenResp
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := a.postForm(ctx, allegroAuthBase+"/token", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {p.DeviceCode},
		}, &tr)
		cancel()
		if err != nil {
			continue
		}
		switch {
		case tr.AccessToken != "":
			err := a.cfg.Update(func(c *Config) {
				acc := AllegroAccount{Name: p.Name, RefreshToken: tr.RefreshToken, AccessToken: tr.AccessToken,
					Expiry: time.Now().Unix() + tr.ExpiresIn}
				for i := range c.AllegroAccounts {
					if c.AllegroAccounts[i].Name == p.Name {
						c.AllegroAccounts[i] = acc
						return
					}
				}
				c.AllegroAccounts = append(c.AllegroAccounts, acc)
			})
			if err != nil {
				set("blad", err.Error())
			} else {
				set("ok", "konto dodane")
			}
			return
		case tr.Error == "authorization_pending":
		case tr.Error == "slow_down":
			interval += 5 * time.Second
		default:
			set("blad", tr.Error+" "+tr.ErrorDesc)
			return
		}
	}
	set("blad", "upłynął czas na potwierdzenie")
}

func DeviceState(userCode string) (state, msg string) {
	devMu.Lock()
	defer devMu.Unlock()
	if p, ok := devPending[userCode]; ok {
		return p.State, p.Msg
	}
	return "brak", ""
}

// token zwraca ważny access token konta, odświeżając go w razie potrzeby.
// Refresh token Allegro jest jednorazowy – nowy zapisujemy od razu.
func (a *Allegro) token(ctx context.Context, name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var acc *AllegroAccount
	c := a.cfg.Get()
	for i := range c.AllegroAccounts {
		if c.AllegroAccounts[i].Name == name {
			acc = &c.AllegroAccounts[i]
		}
	}
	if acc == nil {
		return "", errors.New("brak konta " + name)
	}
	if acc.AccessToken != "" && time.Now().Unix() < acc.Expiry-300 {
		return acc.AccessToken, nil
	}
	var tr tokenResp
	code, err := a.postForm(ctx, allegroAuthBase+"/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {acc.RefreshToken},
	}, &tr)
	if err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("odświeżenie tokenu nieudane (HTTP %d %s) – dodaj konto ponownie", code, tr.Error)
	}
	err = a.cfg.Update(func(c *Config) {
		for i := range c.AllegroAccounts {
			if c.AllegroAccounts[i].Name == name {
				c.AllegroAccounts[i].AccessToken = tr.AccessToken
				c.AllegroAccounts[i].Expiry = time.Now().Unix() + tr.ExpiresIn
				if tr.RefreshToken != "" {
					c.AllegroAccounts[i].RefreshToken = tr.RefreshToken
				}
			}
		}
	})
	return tr.AccessToken, err
}

func (a *Allegro) get(ctx context.Context, account, path string, q url.Values, accept string, out any) error {
	tok, err := a.token(ctx, account)
	if err != nil {
		return err
	}
	u := allegroAPIBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "pl-PL")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("Allegro %s HTTP %d: %s", path, resp.StatusCode, truncate(string(b), 300))
	}
	return json.Unmarshal(b, out)
}

// ---- Zwroty klientów ----

type apiReturn struct {
	ID              string     `json:"id"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       *time.Time `json:"updatedAt"`
	ReferenceNumber string     `json:"referenceNumber"`
	OrderID         string     `json:"orderId"`
	Buyer           struct {
		Login string `json:"login"`
	} `json:"buyer"`
	Items []struct {
		Name     string `json:"name"`
		Quantity int64  `json:"quantity"`
		Reason   struct {
			Type        string `json:"type"`
			UserComment string `json:"userComment"`
		} `json:"reason"`
	} `json:"items"`
	Parcels []struct {
		Waybill               string  `json:"waybill"`
		TransportingWaybill   *string `json:"transportingWaybill"`
		CarrierID             string  `json:"carrierId"`
		TransportingCarrierID *string `json:"transportingCarrierId"`
	} `json:"parcels"`
	Rejection *struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	} `json:"rejection"`
	Status string `json:"status"`
}

func (r apiReturn) toModel(account string) *AReturn {
	m := &AReturn{ID: r.ID, Account: account, ReferenceNumber: r.ReferenceNumber, OrderID: r.OrderID,
		BuyerLogin: r.Buyer.Login, Status: r.Status, CreatedAt: r.CreatedAt}
	if r.UpdatedAt != nil {
		m.UpdatedAt = *r.UpdatedAt
	} else {
		m.UpdatedAt = r.CreatedAt
	}
	for _, it := range r.Items {
		m.Items = append(m.Items, AItem{Name: it.Name, Quantity: it.Quantity, Reason: it.Reason.Type, Comment: it.Reason.UserComment})
	}
	for _, p := range r.Parcels {
		ap := AParcel{Waybill: p.Waybill, CarrierID: p.CarrierID}
		if p.TransportingWaybill != nil {
			ap.TransportingWaybill = *p.TransportingWaybill
		}
		if p.TransportingCarrierID != nil {
			ap.TransportingCarrierID = *p.TransportingCarrierID
		}
		m.Parcels = append(m.Parcels, ap)
	}
	if r.Rejection != nil {
		m.Rejection = strings.TrimSpace(r.Rejection.Code + " " + r.Rejection.Reason)
	}
	return m
}

// Returns pobiera zwroty konta zmienione od "since" (stronicowanie offset/limit, max 1000).
func (a *Allegro) Returns(ctx context.Context, account string, since time.Time) ([]*AReturn, error) {
	var out []*AReturn
	for offset := 0; ; offset += 1000 {
		var resp struct {
			CustomerReturns []apiReturn `json:"customerReturns"`
		}
		q := url.Values{"limit": {"1000"}, "offset": {strconv.Itoa(offset)},
			"updatedAt.gte": {since.UTC().Format(time.RFC3339)}}
		if err := a.get(ctx, account, "/order/customer-returns", q, mediaBeta, &resp); err != nil {
			return out, err
		}
		for _, r := range resp.CustomerReturns {
			out = append(out, r.toModel(account))
		}
		if len(resp.CustomerReturns) < 1000 || offset >= 50000 {
			return out, nil
		}
	}
}

// ReturnsByWaybill – wyszukanie zwrotu po numerze przesyłki (gdy nie ma go jeszcze w pamięci).
func (a *Allegro) ReturnsByWaybill(ctx context.Context, account, code string) ([]*AReturn, error) {
	var out []*AReturn
	for _, field := range []string{"parcels.waybill", "parcels.transportingWaybill"} {
		var resp struct {
			CustomerReturns []apiReturn `json:"customerReturns"`
		}
		if err := a.get(ctx, account, "/order/customer-returns", url.Values{field: {code}}, mediaBeta, &resp); err != nil {
			return out, err
		}
		for _, r := range resp.CustomerReturns {
			out = append(out, r.toModel(account))
		}
		if len(out) > 0 {
			break
		}
	}
	return out, nil
}

// RefundFor – zwroty płatności dla zamówienia (GET /payments/refunds?order.id=).
func (a *Allegro) RefundFor(ctx context.Context, account, orderID string) (*Refund, error) {
	var resp struct {
		Refunds []struct {
			Status     string    `json:"status"`
			CreatedAt  time.Time `json:"createdAt"`
			TotalValue struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"totalValue"`
		} `json:"refunds"`
	}
	if err := a.get(ctx, account, "/payments/refunds", url.Values{"order.id": {orderID}}, mediaPublic, &resp); err != nil {
		return nil, err
	}
	r := &Refund{}
	total := 0.0
	for _, x := range resp.Refunds {
		r.Statuses = append(r.Statuses, x.Status)
		if x.Status == "SUCCESS" || x.Status == "PARTIAL" {
			r.Success = true
			if v, err := strconv.ParseFloat(x.TotalValue.Amount, 64); err == nil {
				total += v
			}
			r.Currency = x.TotalValue.Currency
			if x.CreatedAt.After(r.At) {
				r.At = x.CreatedAt
			}
		}
	}
	if r.Success {
		r.Amount = strconv.FormatFloat(total, 'f', 2, 64)
	}
	return r, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
