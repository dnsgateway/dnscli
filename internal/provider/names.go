package provider

import "strings"

// AbsoluteName turns a name relative to a Zone, as GoDaddy and Namecheap
// write it, into an absolute one: "@" or "" is the apex, and a ".@" suffix
// stands for the Zone.
func AbsoluteName(zone, name string) string {
	switch {
	case name == "@" || name == "":
		return zone
	case strings.HasSuffix(name, ".@"):
		return strings.TrimSuffix(name, "@") + zone
	}

	return name + "." + zone
}

// RelativeName is AbsoluteName undone for GoDaddy and Namecheap: an
// absolute name in a Zone becomes relative to it, "@" for the apex, keeping
// the name's own case. A name outside the Zone is returned as given,
// without a trailing dot.
func RelativeName(zone, name string) string {
	name = strings.TrimSuffix(name, ".")
	lower, z := strings.ToLower(name), strings.ToLower(strings.TrimSuffix(zone, "."))

	switch {
	case lower == z:
		return "@"
	case strings.HasSuffix(lower, "."+z):
		return name[:len(name)-len(z)-1]
	}

	return name
}
