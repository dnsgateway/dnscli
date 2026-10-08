// Package provider is dnscli's thin layer over the dnsprovider module
// (ADR-0006): it reads the credentials dnscli seals for an Account into
// its Provider's field map, opens the Provider from the module's Registry
// with them, and converts between the module's types and dnscli's cache
// types (internal/dns).
package provider
