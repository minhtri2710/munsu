package home

// Store is a mailbox store.
func SomethingDeclared() {}

func AfterListFence() {}

func AfterNestedListFence() {}

type Store struct {
	ParentHome string
}

// WriteEnvelope appends a message.
func (s *Store) WriteEnvelope(env string) error { return nil }

// ReadEnvelope reads a message back.
func (s *Store) ReadEnvelope(id string) (string, error) { return "", nil }
