package home

// Store is a mailbox store.
type Store struct {
	ParentHome string
}

type Foo[T any] struct{}
type Bar[A, B any] struct{}
type Outer struct {
	Foo[int]
	Bar[string, int]
}

// WriteEnvelope appends a message.
func (s *Store) WriteEnvelope(env string) error { return nil }

// ReadEnvelope reads a message back.
func (s *Store) ReadEnvelope(id string) (string, error) { return "", nil }
