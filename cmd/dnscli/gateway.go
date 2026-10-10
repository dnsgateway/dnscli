package main

import (
	"crypto/x509"
	"fmt"
	"maps"
	"os"

	"github.com/dnsgateway/dnsgate/gatewayadapter"
	"github.com/dnsgateway/dnsprovider"
)

// providerRegistry returns the Provider types of this build with the
// Gateway Adapter as "dnsgate", verifying every Gateway against the
// certificates of caFiles on top of the system's.
func providerRegistry(caFiles []string) (dnsprovider.Registry, error) {
	pool, err := gatewayRoots(caFiles)
	if err != nil {
		return nil, err
	}

	r := maps.Clone(registry)
	r["dnsgate"] = gatewayadapter.Factory(gatewayadapter.WithRootCAs(pool))

	return r, nil
}

// gatewayRoots returns the system's CA certificates with those of every
// PEM file in caFiles added, since one person may hold Accounts for both
// public and private Gateways. Without caFiles it returns nil, so the
// system's apply.
func gatewayRoots(caFiles []string) (*x509.CertPool, error) {
	if len(caFiles) == 0 {
		return nil, nil
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}

	for _, file := range caFiles {
		pem, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}

		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file %s holds no PEM certificate", file)
		}
	}

	return pool, nil
}
