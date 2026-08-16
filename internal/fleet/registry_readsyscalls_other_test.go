//go:build !linux

package fleet

// readSyscalls has no portable equivalent outside Linux: darwin's rusage block
// counters read zero whenever the page cache serves the read, so they cannot
// count doc reads. Callers report the guard as not asserted rather than
// substituting a wall-clock proxy for it.
func readSyscalls() (int64, bool) { return 0, false }
