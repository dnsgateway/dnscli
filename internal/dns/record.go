package dns

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// EditableTypes are the Record types dnscli can add and edit, in the
// order the Record form offers them. Values are in zone-file presentation
// form, so MX, SRV and URI put the priority first:
//
//	A      an IPv4 address                  192.0.2.1
//	AAAA   an IPv6 address                  2001:db8::1
//	CNAME  a host name                      www.example.com
//	MX     preference, mail host            10 mail.example.com ("0 ." is a null MX)
//	TXT    any non-empty text               v=spf1 -all
//	NS     a host name                      ns1.example.net
//	SRV    priority, weight, port, target   10 5 443 host.example.com
//	CAA    flags, tag, value                0 issue "letsencrypt.org"
//	URI    priority, weight, target         10 1 "https://example.com/"
var EditableTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA", "URI"}

// Editable reports whether dnscli can add and edit Records of a type.
func Editable(typ string) bool {
	return slices.Contains(EditableTypes, typ)
}

// TTLAuto is Cloudflare's "automatic" TTL. Any other TTL must be in the
// Provider's TTLRange, by default between MinTTL and MaxTTL seconds.
const (
	TTLAuto = 1
	MinTTL  = 30
	MaxTTL  = 86400
)

// TTLRange is the shortest and the longest TTL a Provider takes, in seconds.
type TTLRange struct {
	Min, Max uint32
}

// DefaultTTLRange is the TTLRange of a Provider that does not say.
var DefaultTTLRange = TTLRange{Min: MinTTL, Max: MaxTTL}

// Check reports whether a TTL is TTLAuto or in range.
func (r TTLRange) Check(ttl uint32) error {
	if ttl == TTLAuto || (ttl >= r.Min && ttl <= r.Max) {
		return nil
	}

	return fmt.Errorf("the TTL must be %d (automatic) or between %d and %d seconds", TTLAuto, r.Min, r.Max)
}

// QualifyName turns a Record name typed for a Zone into the absolute name
// dnscli stores, without a trailing dot. "@" or an empty name is the Zone
// apex. A name ending in a dot is absolute and must be inside the Zone. So
// is the zone name itself, or a name ending in "."+zone. Any other name is
// relative to the Zone. A leading "*" label makes a wildcard name.
func QualifyName(zone, name string) (string, error) {
	zone = strings.TrimSuffix(zone, ".")
	name = strings.TrimSpace(name)

	// DNS names ignore case: compare them in lower case, keep them as typed.
	inZone := func(n string) bool {
		n, z := strings.ToLower(n), strings.ToLower(zone)

		return n == z || strings.HasSuffix(n, "."+z)
	}

	var abs string

	switch {
	case name == "" || name == "@":
		return zone, nil
	case strings.HasSuffix(name, "."):
		abs = strings.TrimSuffix(name, ".")
		if !inZone(abs) {
			return "", fmt.Errorf("%s is not inside the Zone %s", name, zone)
		}
	case inZone(name):
		abs = name
	default:
		abs = name + "." + zone
	}

	if err := checkHostname(abs, true); err != nil {
		return "", fmt.Errorf("the name %s: %w", name, err)
	}

	return abs, nil
}

// RelativeName is a Record name as the Record form shows it: relative to
// the Zone, "@" for the apex. A name outside the Zone stays absolute, with
// a trailing dot.
func RelativeName(zone, name string) string {
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	lower := strings.ToLower(strings.TrimSuffix(name, "."))

	switch {
	case lower == zone:
		return "@"
	case strings.HasSuffix(lower, "."+zone):
		return name[:len(lower)-len(zone)-1]
	}

	return strings.TrimSuffix(name, ".") + "."
}

// CheckValue reports whether a Record value is in the format its type
// takes; see EditableTypes.
func CheckValue(typ, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("enter a value")
	}

	f := strings.Fields(value)

	switch typ {
	case "A":
		if a, err := netip.ParseAddr(value); err != nil || !a.Is4() {
			return fmt.Errorf("an A value is an IPv4 address, such as 192.0.2.1, not %q", value)
		}
	case "AAAA":
		if a, err := netip.ParseAddr(value); err != nil || !a.Is6() || a.Is4In6() || a.Zone() != "" {
			return fmt.Errorf("an AAAA value is an IPv6 address, such as 2001:db8::1, not %q", value)
		}
	case "CNAME", "NS":
		if len(f) != 1 || checkHostname(value, false) != nil {
			return fmt.Errorf("%s %s value is a host name, such as host.example.com, not %q", map[string]string{"CNAME": "a", "NS": "an"}[typ], typ, value)
		}
	case "MX":
		if len(f) != 2 || !isUint16(f[0]) || (f[1] != "." && checkHostname(f[1], false) != nil) {
			return fmt.Errorf("an MX value is a preference and a mail host, such as 10 mail.example.com, not %q", value)
		}
	case "TXT":
		// Any text.
	case "SRV":
		if len(f) != 4 || !isUint16(f[0]) || !isUint16(f[1]) || !isUint16(f[2]) || (f[3] != "." && checkHostname(f[3], false) != nil) {
			return fmt.Errorf("an SRV value is a priority, weight, port and target, such as 10 5 443 host.example.com, not %q", value)
		}
	case "CAA":
		if len(f) < 3 || !isUint8(f[0]) || !isAlnum(f[1]) {
			return fmt.Errorf(`a CAA value is flags, a tag and a value, such as 0 issue "letsencrypt.org", not %q`, value)
		}
	case "URI":
		if len(f) != 3 || !isUint16(f[0]) || !isUint16(f[1]) || len(f[2]) < 3 || !strings.HasPrefix(f[2], `"`) || !strings.HasSuffix(f[2], `"`) {
			return fmt.Errorf(`a URI value is a priority, weight and quoted target, such as 10 1 "https://example.com/", not %q`, value)
		}
	default:
		return fmt.Errorf("dnscli cannot edit %s Records", typ)
	}

	return nil
}

// checkHostname checks a DNS name: at most 253 characters, labels of 1 to
// 63 letters, digits, hyphens or underscores, and a trailing dot allowed.
// A Record name may also start with a "*" label, for a wildcard.
func checkHostname(name string, wildcard bool) error {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 {
		return errors.New("is not a valid DNS name")
	}

	for i, label := range strings.Split(name, ".") {
		if wildcard && i == 0 && label == "*" {
			continue
		}

		if label == "" || len(label) > 63 {
			return errors.New("is not a valid DNS name")
		}

		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return fmt.Errorf("cannot contain %q", r)
			}
		}
	}

	return nil
}

func isUint16(s string) bool {
	_, err := strconv.ParseUint(s, 10, 16)

	return err == nil
}

func isUint8(s string) bool {
	_, err := strconv.ParseUint(s, 10, 8)

	return err == nil
}

func isAlnum(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}

	return true
}
