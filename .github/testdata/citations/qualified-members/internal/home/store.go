package home

type Store struct {
	Nested struct {
		NestedField string
	}
}

func Ready() {}

func (s *Store) WriteEnvelope() {}
func (s *Store) ReadEnvelope() {}
