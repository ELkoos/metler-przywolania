package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeAllegro udaje auth i API Allegro wg swaggera (customer-returns, payments/refunds).
func fakeAllegro(t *testing.T) (*httptest.Server, *int) {
	refreshCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Error("brak Basic auth")
		}
		_ = r.ParseForm()
		switch r.FormValue("grant_type") {
		case "refresh_token":
			refreshCalls++
			if r.FormValue("refresh_token") != "R1" && r.FormValue("refresh_token") != "R2" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"A","refresh_token":"R2","expires_in":43199}`))
		case "urn:ietf:params:oauth:grant-type:device_code":
			_, _ = w.Write([]byte(`{"access_token":"A","refresh_token":"R1","expires_in":43199}`))
		}
	})
	mux.HandleFunc("/auth/oauth/device", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"D","user_code":"abc","verification_uri_complete":"https://allegro.pl/skojarz-aplikacje?code=abc","expires_in":60,"interval":1}`))
	})
	mux.HandleFunc("/order/customer-returns", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != mediaBeta || r.Header.Get("Authorization") != "Bearer A" {
			w.WriteHeader(406)
			return
		}
		q := r.URL.Query()
		all := []map[string]any{
			{"id": "ret-1", "createdAt": "2026-11-10T10:00:00Z", "updatedAt": "2026-11-15T10:00:00Z", "referenceNumber": "1234/Z04A",
				"orderId": "ord-1", "buyer": map[string]any{"login": "kupujacy1"}, "status": "DELIVERED",
				"items":   []any{map[string]any{"name": "Wiertarka", "quantity": 1, "reason": map[string]any{"type": "DONT_LIKE_IT"}}},
				"parcels": []any{map[string]any{"waybill": "AD00R28X72", "carrierId": "ALLEGRO", "transportingWaybill": "2171143568953", "transportingCarrierId": "ORLEN"}}},
			{"id": "ret-2", "createdAt": "2026-11-11T10:00:00Z", "updatedAt": nil, "referenceNumber": "5555/Z04A",
				"orderId": "ord-2", "buyer": map[string]any{"login": "kupujacy2"}, "status": "DELIVERED",
				"parcels": []any{map[string]any{"waybill": "620000111222333444555666", "carrierId": "INPOST", "transportingWaybill": nil}}},
		}
		out := []map[string]any{}
		for _, r := range all {
			p := r["parcels"].([]any)[0].(map[string]any)
			if w := q.Get("parcels.waybill"); w != "" && p["waybill"] != w {
				continue
			}
			if w := q.Get("parcels.transportingWaybill"); w != "" && p["transportingWaybill"] != w {
				continue
			}
			out = append(out, r)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": len(out), "customerReturns": out})
	})
	mux.HandleFunc("/payments/refunds", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != mediaPublic {
			w.WriteHeader(406)
			return
		}
		if r.URL.Query().Get("order.id") == "ord-2" {
			_, _ = w.Write([]byte(`{"refunds":[{"id":"x","status":"SUCCESS","createdAt":"2026-11-16T10:00:00Z","totalValue":{"amount":"99.90","currency":"PLN"}}],"count":1,"totalCount":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"refunds":[],"count":0,"totalCount":0}`))
	})
	srv := httptest.NewServer(mux)
	allegroAuthBase = srv.URL + "/auth/oauth"
	allegroAPIBase = srv.URL
	return srv, &refreshCalls
}

func fakeBase(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-BLToken") != "BT" {
			_, _ = w.Write([]byte(`{"status":"ERROR","error_code":"ERROR_BAD_TOKEN","error_message":"bad"}`))
			return
		}
		_ = r.ParseForm()
		switch r.FormValue("method") {
		case "getOrderReturns":
			var p map[string]any
			_ = json.Unmarshal([]byte(r.FormValue("parameters")), &p)
			if p["id_from"] != nil {
				_, _ = w.Write([]byte(`{"status":"SUCCESS","returns":[]}`))
				return
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(`{"status":"SUCCESS","returns":[
			 {"return_id":11,"order_id":901,"external_order_id":"ord-1","reference_number":"R629/2026","order_return_source":"allegro","status_id":12757,"date_add":NOWTS,"delivery_package_nr":"","products":[{"name":"Wiertarka","sku":"S1","ean":"590","quantity":1,"status_id":8020}]},
			 {"return_id":12,"order_id":"902","external_order_id":"ord-x","reference_number":"R630/2026","status_id":"12758","date_add":"NOWTS","delivery_package_nr":"00340434","products":[]}
			]}`, "NOWTS", strconv.FormatInt(time.Now().Unix(), 10))))
		case "getOrderReturnStatusList":
			_, _ = w.Write([]byte(`{"status":"SUCCESS","statuses":[{"id":12757,"name":"Nowe"},{"id":12758,"name":"Odebrane"}]}`))
		default:
			_, _ = w.Write([]byte(`{"status":"ERROR","error_code":"ERROR_UNKNOWN_METHOD"}`))
		}
	}))
}

func setup(t *testing.T) (*ConfigStore, *Store, *Syncer, *int) {
	dir := t.TempDir()
	cfg, _, err := OpenConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	asrv, calls := fakeAllegro(t)
	t.Cleanup(asrv.Close)
	bsrv := fakeBase(t)
	t.Cleanup(bsrv.Close)
	baseURL = bsrv.URL
	_ = cfg.Update(func(c *Config) {
		c.AllegroClientID, c.AllegroClientSecret = "cid", "sec"
		c.AllegroAccounts = []AllegroAccount{{Name: "konto1", RefreshToken: "R1"}}
		c.BaseToken = "BT"
		c.Workers = []string{"Ania"}
	})
	b := NewBase(cfg)
	b.gap = 0
	return cfg, st, NewSyncer(cfg, st, NewAllegro(cfg), b), calls
}

func TestNormCode(t *testing.T) {
	if got := NormCode(" ad00-r28x72 \r"); got != "AD00R28X72" {
		t.Fatal(got)
	}
}

func TestSyncMatchAndAlerts(t *testing.T) {
	cfg, st, sy, calls := setup(t)
	sy.Once(context.Background())
	if *calls != 1 {
		t.Fatalf("odświeżenie tokenu: %d", *calls)
	}
	if got := cfg.Get().AllegroAccounts[0].RefreshToken; got != "R2" {
		t.Fatalf("nowy refresh token nie zapisany: %s", got)
	}
	if len(st.st.Allegro) != 2 || len(st.st.Base) != 2 || st.st.BaseStatuses[12757] != "Nowe" {
		t.Fatalf("sync: %d %d %v errors %v", len(st.st.Allegro), len(st.st.Base), st.st.BaseStatuses, st.st.SyncErrors)
	}
	if len(st.st.SyncErrors) != 0 {
		t.Fatal(st.st.SyncErrors)
	}
	now := time.Now()
	// skan numeru przewoźnika (Orlen) → zwrot Allegro ret-1 + Base 11 przez external_order_id
	sc, _ := st.AddScan("2171143568953", "Orlen Paczka", "Ania", now.AddDate(0, 0, -3))
	if sc.AllegroID != "ret-1" || sc.BaseID != 11 || sc.Match != "allegro-przewoznik" {
		t.Fatalf("%+v", sc)
	}
	// numer listu Allegro
	sc2, _ := st.AddScan("AD00R28X72", "Allegro One", "Ania", now)
	if sc2.AllegroID != "ret-1" || sc2.Duplicate {
		t.Fatalf("%+v", sc2)
	}
	// Base-only numer
	sc3, _ := st.AddScan("00340434", "DPD", "Ania", now)
	if sc3.BaseID != 12 || sc3.AllegroID != "" {
		t.Fatalf("%+v", sc3)
	}
	// nieznany
	sc4, _ := st.AddScan("XYZ999", "DPD", "Ania", now)
	if sc4.Matched() {
		t.Fatal("nie powinien pasować")
	}
	if n := len(st.Scans(time.Time{}, true)); n != 1 {
		t.Fatalf("do wyjaśnienia: %d", n)
	}
	// ręczne wyjaśnienie po numerze Base
	if err := st.ResolveScan(sc4.ID, "R629/2026", ""); err != nil {
		t.Fatal(err)
	}
	if n := len(st.Scans(time.Time{}, true)); n != 0 {
		t.Fatalf("po wyjaśnieniu: %d", n)
	}

	// zwroty płatności sprawdzane dla zeskanowanych
	sy.Once(context.Background())
	if st.st.Allegro["ret-1"].Refund == nil || st.st.Allegro["ret-1"].Refund.Success {
		t.Fatal("ret-1 bez zwrotu środków")
	}

	_ = cfg.Update(func(c *Config) {
		c.DeadlineDays = 4
		c.DeadlineWarnDays = 2
		c.RefundAlertDays = 2
		c.NotScannedDays = 0
	})
	al := st.Alerts(cfg.Get(), time.Now().Add(time.Second))
	var srodki, niezesk int
	for _, a := range al {
		switch a.Kind {
		case "srodki":
			srodki++
			if a.Allegro.ID != "ret-1" || a.Level != "czerwony" || a.DaysLeft == nil || *a.DaysLeft > 1 {
				t.Fatalf("alert środki: %+v left=%v", a, *a.DaysLeft)
			}
		case "niezeskanowana":
			niezesk++
			if a.Allegro.ID != "ret-2" {
				t.Fatalf("%+v", a)
			}
		}
	}
	if srodki != 1 || niezesk != 1 {
		t.Fatalf("alerty: %+v", al)
	}
	// zeskanowanie ret-2 (InPost) → sprawdzenie zwrotu płatności → SUCCESS, brak alertu
	st.AddScan("620000111222333444555666", "InPost", "Ania", now)
	sy.Once(context.Background())
	for _, a := range st.Alerts(cfg.Get(), now) {
		if a.Allegro != nil && a.Allegro.ID == "ret-2" {
			t.Fatalf("ret-2 zwrócony, a alert jest: %+v", a)
		}
	}
}

func TestScanEndpointLiveLookup(t *testing.T) {
	cfg, st, sy, _ := setup(t)
	mux := http.NewServeMux()
	routes(mux, cfg, st, sy)
	// bez synchronizacji – skan musi znaleźć zwrot zapytaniem do Allegro po numerze
	body, _ := json.Marshal(map[string]string{"code": "2171143568953", "batch": "Orlen Paczka", "worker": "Ania"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/skan", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var v ScanView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Allegro == nil || v.Allegro.ReferenceNumber != "1234/Z04A" {
		t.Fatalf("%s", rec.Body.String())
	}
	// bez osoby – błąd
	body, _ = json.Marshal(map[string]string{"code": "1"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/skan", bytes.NewReader(body)))
	if rec.Code != 400 {
		t.Fatal("brak walidacji osoby")
	}
	// CSV
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/eksport.csv", nil))
	if !strings.Contains(rec.Body.String(), "1234/Z04A") {
		t.Fatal(rec.Body.String())
	}
	// panel wymaga hasła
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
	if rec.Code != 401 {
		t.Fatal("panel bez hasła")
	}
	req := httptest.NewRequest("GET", "/admin", nil)
	req.SetBasicAuth("x", cfg.Get().AdminPassword)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "konto1") {
		t.Fatal(rec.Code)
	}
}

func TestDeviceFlow(t *testing.T) {
	cfg, _, sy, _ := setup(t)
	p, err := sy.allegro.StartDevice(context.Background(), "konto2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.VerificationURIComplete, "code=abc") {
		t.Fatal(p.VerificationURIComplete)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := DeviceState("abc"); s == "ok" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s, m := DeviceState("abc"); s != "ok" {
		t.Fatal(s, m)
	}
	if n := len(cfg.Get().AllegroAccounts); n != 2 {
		t.Fatal(n)
	}
	_ = url.Values{}
}

func TestDuplicateScan(t *testing.T) {
	_, st, _, _ := setup(t)
	a, _ := st.AddScan("ABC123", "DPD", "Ania", time.Now())
	b, _ := st.AddScan("abc-123", "DPD", "Ania", time.Now())
	if a.Duplicate || !b.Duplicate {
		t.Fatal(a.Duplicate, b.Duplicate)
	}
}
