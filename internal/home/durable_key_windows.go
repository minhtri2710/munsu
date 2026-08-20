//go:build windows

package home

import (
	"fmt"
	"strings"
)

// durableKey returns the NTFS/Win32-safe file stem (no extension) used to
// persist a logical task key (id) on Windows. Win32 forbids the bytes
// < > : " / \ | ? * and control characters in filenames, treats ':' as an
// alternate-data-stream separator, trims trailing dots and spaces, and
// reserves the device names CON, PRN, AUX, NUL, COM1-9, LPT1-9
// case-insensitively.
//
// Every byte outside the safe alphabet [A-Za-z0-9_-] is percent-escaped as
// %XX (and '%' as %25), so the result is a plain reversible filename that is
// injective over the logical key domain: no two keys map to the same stem,
// and the reverse mapping unambiguously recovers the original key. The stem
// never contains a ':', a trailing dot/space, or any other Win32-forbidden
// byte. Captain keys always encode to a stem containing '%', so they can
// never collide with a reserved device name; a bare key that would fall back
// to one (for example a task id of "CON") fails closed rather than persisting
// an uncreatable name.
//
// NTFS is case-insensitive, so two logical keys differing only in case (which
// munsu does not mint) would share one file on disk; that is a property of the
// filesystem, not of this mapping.
func durableKey(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("durable key: empty id")
	}
	var b strings.Builder
	b.Grow(len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if isDurableSafeByte(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	stem := b.String()
	if WindowsReservedName(stem) {
		return "", fmt.Errorf("durable key %q maps to a reserved Windows device name", id)
	}
	return stem, nil
}

// reverseDurableKey returns the logical task key for a persisted file stem
// created by durableKey. A stem that is not a well-formed encoding (a stray
// or truncated escape, or an unescaped byte the encoder would never emit)
// fails closed instead of guessing a key.
func reverseDurableKey(stem string) (string, error) {
	var b strings.Builder
	b.Grow(len(stem))
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		switch {
		case c == '%':
			if i+2 >= len(stem) {
				return "", fmt.Errorf("durable key: truncated escape in %q", stem)
			}
			hi, ok1 := hexVal(stem[i+1])
			lo, ok2 := hexVal(stem[i+2])
			if !ok1 || !ok2 {
				return "", fmt.Errorf("durable key: malformed escape in %q", stem)
			}
			b.WriteByte(hi<<4 | lo)
			i += 2
		case isDurableSafeByte(c):
			b.WriteByte(c)
		default:
			return "", fmt.Errorf("durable key: unescaped byte %q in %q", c, stem)
		}
	}
	return b.String(), nil
}

func isDurableSafeByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_' || c == '-'
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// WindowsReservedName reports whether a bare file stem (before any extension)
// is a Windows reserved device name, compared case-insensitively.
func WindowsReservedName(stem string) bool {
	switch {
	case strings.EqualFold(stem, "CON"), strings.EqualFold(stem, "PRN"),
		strings.EqualFold(stem, "AUX"), strings.EqualFold(stem, "NUL"):
		return true
	}
	if len(stem) < 4 {
		return false
	}
	prefix := stem[:3]
	rest := stem[3:]
	if (strings.EqualFold(prefix, "COM") || strings.EqualFold(prefix, "LPT")) && len(rest) == 1 && rest[0] >= '1' && rest[0] <= '9' {
		return true
	}
	return false
}
