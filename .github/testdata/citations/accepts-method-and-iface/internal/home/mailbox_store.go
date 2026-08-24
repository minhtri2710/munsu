package home

// Digester digests target events.
type Digester interface {
	SetTargetSafety()
}

// Store is a mailbox store.
type Store struct {
	ParentHome string
}

// WriteEnvelope appends a message.
func (s *Store) WriteEnvelope(env string) error { return nil }

// ReadEnvelope reads a message back.
func (s *Store) ReadEnvelope(id string) (string, error) { return "", nil }
