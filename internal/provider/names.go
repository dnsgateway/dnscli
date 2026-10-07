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
