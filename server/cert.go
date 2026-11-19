package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ensureCert zwraca ścieżki certyfikatu TLS. Gdy ich brak, tworzy certyfikat własny (10 lat)
// dla adresów IP tej maszyny. Aplikacja przypina odcisk klucza publicznego (pin), więc nie jest
// potrzebny urząd certyfikacji ani domena.
func ensureCert(dir string) (string, string, error) {
	crt, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if _, err := os.Stat(crt); os.IsNotExist(err) {
		if err := generateCert(crt, key, localIPs()); err != nil {
			return "", "", err
		}
		log.Printf("utworzono certyfikat %s", crt)
	}
	pair, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		return "", "", err
	}
	if c, err := x509.ParseCertificate(pair.Certificate[0]); err == nil {
		log.Printf("certyfikat TLS: %v, ważny do %s, pin aplikacji: %s", c.IPAddresses, c.NotAfter.Format("2006-01-02"), SPKIPin(c))
	}
	return crt, key, nil
}

// SPKIPin: base64(SHA-256(klucz publiczny)), ten sam format co pin w aplikacji ("sha256/...").
func SPKIPin(c *x509.Certificate) string {
	h := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return "sha256/" + base64.StdEncoding.EncodeToString(h[:])
}

func generateCert(crtPath, keyPath string, ips []net.IP) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "przywolania", Organization: []string{"Przywolania"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func localIPs() []net.IP {
	ips := []net.IP{net.ParseIP("127.0.0.1")}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}
