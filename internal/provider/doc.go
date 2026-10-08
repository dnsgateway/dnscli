// Package provider is dnscli's thin layer over the dnsprovider module
// (ADR-0006): it opens an Account's Provider from the module's Registry
// with the API token dnscli stores, and converts between the module's
// types and dnscli's cache types (internal/dns).
package provider
