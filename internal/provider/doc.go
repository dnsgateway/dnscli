// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package provider is dnscli's thin layer over the dnsprovider module
// (ADR-0006): it reads the credentials dnscli seals for an Account into
// its Provider's field map, opens the Provider from the module's Registry
// with them, and converts between the module's types and dnscli's cache
// types (internal/dns).
package provider
