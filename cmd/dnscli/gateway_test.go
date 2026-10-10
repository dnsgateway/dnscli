package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dnsgateway/dnsgate/gatewayadapter"
)

// testCA is a self-signed CA certificate, written to a PEM file, with a
// leaf certificate for gateway.test that it signs.
type testCA struct {
	file string
	leaf *x509.Certificate
}

func newTestCA(t *testing.T, name string) testCA {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		DNSNames:     []string{"gateway.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), name+".pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	return testCA{file: file, leaf: leaf}
}

func TestTheRegistryOffersTheGatewayAdapter(t *testing.T) {
	r, err := providerRegistry(nil)
	if err != nil {
		t.Fatalf("providerRegistry: %v", err)
	}

	f, ok := r["dnsgate"]
	if !ok {
		t.Fatal(`the registry has no "dnsgate" Provider type`)
	}

	if !reflect.DeepEqual(f.Fields, gatewayadapter.Factory().Fields) {
		t.Fatalf("dnsgate fields = %+v, want the Gateway Adapter's", f.Fields)
	}

	if _, ok := r["cloudflare"]; !ok {
		t.Fatal("the registry lost the dnsprovider Provider types")
	}
}

func TestGatewayRootsTrustEveryCAFileOnTopOfTheSystemRoots(t *testing.T) {
	first, second := newTestCA(t, "first"), newTestCA(t, "second")

	pool, err := gatewayRoots([]string{first.file, second.file})
	if err != nil {
		t.Fatalf("gatewayRoots: %v", err)
	}

	for _, ca := range []testCA{first, second} {
		if _, err := ca.leaf.Verify(x509.VerifyOptions{DNSName: "gateway.test", Roots: pool}); err != nil {
			t.Errorf("a Gateway certificate signed by %s is not trusted: %v", ca.file, err)
		}
	}

	system, err := x509.SystemCertPool()
	if err != nil {
		t.Skipf("no system roots: %v", err)
	}

	for _, ca := range []testCA{first, second} {
		pem, _ := os.ReadFile(ca.file)
		system.AppendCertsFromPEM(pem)
	}

	if !pool.Equal(system) {
		t.Fatal("the CA files replaced the system roots instead of adding to them")
	}
}

func TestWithoutCAFilesAGatewayIsVerifiedByTheSystemRoots(t *testing.T) {
	pool, err := gatewayRoots(nil)
	if err != nil || pool != nil {
		t.Fatalf("gatewayRoots(nil) = %v, %v; want no pool, so the system roots apply", pool, err)
	}
}

func TestGatewayRootsRefuseABadCAFile(t *testing.T) {
	good := newTestCA(t, "good")

	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("no certificate here"), 0o600); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(t.TempDir(), "missing.pem")

	for _, bad := range []string{empty, missing} {
		if _, err := gatewayRoots([]string{good.file, bad}); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("gatewayRoots error = %v, want one naming %s", err, bad)
		}
	}
}
