package domain

import (
	"fmt"
	"strings"
)

// Words is the record of a Human's words behind a decision: who gave them
// (Grantor), on which channel, and the verbatim Quote. It is a claim made by
// the caller that records it; munsu never verifies it as authority.
type Words struct {
	Grantor string `json:"grantor"`
	Channel string `json:"channel"`
	Quote   string `json:"quote"`
}

// Validate refuses a record with no grantor, no channel or an empty quote.
func (w Words) Validate() error {
	switch {
	case strings.TrimSpace(w.Grantor) == "":
		return fmt.Errorf("words: grantor is required")
	case strings.TrimSpace(w.Channel) == "":
		return fmt.Errorf("words: channel is required")
	case strings.TrimSpace(w.Quote) == "":
		return fmt.Errorf("words: quote is required")
	}
	return nil
}
