package integrate

type testMunsuResolver struct {
	path string
	err  error
}

func (r testMunsuResolver) Resolve() (string, error) {
	return r.path, r.err
}
