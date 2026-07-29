package orchestrator

import "strings"

// FM_INJECT_MARK is the sentinel byte used to mark inject payloads.
// U+2063 INVISIBLE SEPARATOR is an invisible, zero-width Unicode character
// that can be embedded in any text stream without affecting display.
const FM_INJECT_MARK = "\u2063"

// Mark prepends the inject sentinel to a payload string.
func Mark(payload string) string {
	return FM_INJECT_MARK + payload
}

// Marked checks whether a line begins with the inject sentinel.
func Marked(line string) bool {
	return strings.HasPrefix(line, FM_INJECT_MARK)
}
