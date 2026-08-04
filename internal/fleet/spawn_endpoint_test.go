package fleet

// fakeEndpointCapabilities is the shared reservation-aware test double: it
// satisfies the single mandatory EndpointCapabilities contract via
// CreateReserved (find-or-create under the exact reservation identity is
// exercised by reentrantEndpointCapabilities in the launch tests; this fake
// delegates to its backend's NewWindow for the preflight/integration tests
// that only exercise first-attempt creation).
type fakeEndpointCapabilities struct{ backend *fakeBackend }

func (f fakeEndpointCapabilities) CreateReserved(req CreateRequest) (CreatedEndpoint, error) {
	id, err := f.backend.NewWindow(req.Home, req.TabName)
	return CreatedEndpoint{Backend: "test", Handle: id}, err
}
func (f fakeEndpointCapabilities) Submit(ep CreatedEndpoint, text string) error {
	return f.backend.SendKeys(ep.Handle, text)
}
func (f fakeEndpointCapabilities) Probe(ep CreatedEndpoint) (SpawnEndpointObservation, error) {
	if f.backend.Alive(ep.Handle) {
		return SpawnEndpointObservation{State: EndpointAlive}, nil
	}
	return SpawnEndpointObservation{State: EndpointDead}, nil
}
func (f fakeEndpointCapabilities) Capture(ep CreatedEndpoint, n int) (string, error) {
	return f.backend.Capture(ep.Handle, n)
}
func (f fakeEndpointCapabilities) Dispose(ep CreatedEndpoint) error {
	return f.backend.Teardown(ep.Handle)
}
func runnerEndpoint(fake *fakeBackend) (EndpointCapabilities, CreatedEndpoint) {
	return fakeEndpointCapabilities{backend: fake}, CreatedEndpoint{Backend: "test", Handle: "win-1"}
}
