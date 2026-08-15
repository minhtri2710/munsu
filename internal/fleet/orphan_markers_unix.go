//go:build darwin || linux

package fleet

import "strings"

// Reading a process environment is the platform inventory's half of the orphan
// scan, and only darwin and linux have one: orphan_inventory_other.go answers
// ErrProcessInventoryUnsupported without ever looking at an environment. These
// two helpers carry the constraint of their only callers so the build that
// cannot use them does not compile them either -- the reachability lane reads
// the union over every GOOS, and a helper unreachable in one leg of that union
// is either dead weight in that build or a hole in it. Here it is the first.

// keepMarkers copies the whitelisted keys out of a raw KEY=VALUE environment
// block and drops everything else, so no credential ever leaves the scan.
func keepMarkers(environment []string) map[string]string {
	markers := make(map[string]string)
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		key := entry[:separator]
		if !orphanMarkerKeys[key] {
			continue
		}
		markers[key] = entry[separator+1:]
	}
	return markers
}

// hasOwnershipMarker reports whether a process declares an owning run. A
// process without one is out of the scan's reach entirely — it is not reported
// as UNKNOWN, because that would make every process on the machine a finding.
func hasOwnershipMarker(markers map[string]string) bool {
	return strings.TrimSpace(markers[MarkerMulticaTask]) != "" || strings.TrimSpace(markers[MarkerMunsuTask]) != ""
}
