// Package marker owns the General→Captain request marker.
//
// A Captain is itself a munsu home, so unmarked pane input looks like the
// human captain typing. Marked sends tell the Captain to answer via the parent
// status file (and optional doc pointer), never chat-only — the General does
// not read Captain chat.
package home

import "strings"

// FromGeneralLabel is the greppable visible half of the marker.
const FromGeneralLabel = "[mu-from-general]"

// FromGeneralSeparator is U+2063 INVISIBLE SEPARATOR (UTF-8 e2 81 a3).
// It has no normal keyboard keystroke and survives Herdr/tmux text injection.
const FromGeneralSeparator = "\u2063"

// FromGeneralMark is the full prefix prepended to General-routed Captain requests.
const FromGeneralMark = FromGeneralLabel + FromGeneralSeparator

// IsFromGeneral reports whether message begins with the full marker.
func IsFromGeneral(message string) bool {
	return strings.HasPrefix(message, FromGeneralMark)
}

// MarkFromGeneral returns message with exactly one leading FromGeneralMark.
func MarkFromGeneral(message string) string {
	if IsFromGeneral(message) {
		return message
	}
	return FromGeneralMark + message
}
