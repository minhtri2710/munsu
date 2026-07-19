// Package marker owns the Marshal→Second request marker.
//
// A Second is itself a munsu home, so unmarked pane input looks like the
// captain typing. Marked sends tell the Second to answer via the parent status
// file (and optional doc pointer), never chat-only — the Marshal does not read
// Second chat.
package marker

import "strings"

// FromMarshalLabel is the greppable visible half of the marker.
const FromMarshalLabel = "[mu-from-marshal]"

// FromMarshalSeparator is U+2063 INVISIBLE SEPARATOR (UTF-8 e2 81 a3).
// It has no normal keyboard keystroke and survives Herdr/tmux text injection.
const FromMarshalSeparator = "\u2063"

// FromMarshalMark is the full prefix prepended to Marshal-routed Second requests.
const FromMarshalMark = FromMarshalLabel + FromMarshalSeparator

// IsFromMarshal reports whether message begins with the full marker.
func IsFromMarshal(message string) bool {
	return strings.HasPrefix(message, FromMarshalMark)
}

// MarkFromMarshal returns message with exactly one leading FromMarshalMark.
func MarkFromMarshal(message string) string {
	if IsFromMarshal(message) {
		return message
	}
	return FromMarshalMark + message
}
