package home

// WriterInventory is the quiescence evidence returned by a writer fence.
// The fleet writer fence (internal/fleet) produces it after verifying no
// typed artifacts or OS writer processes remain for a home.
type WriterInventory struct {
	VerifiedQuiescent bool     `json:"verified_quiescent"`
	Writers           []string `json:"writers,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
}
