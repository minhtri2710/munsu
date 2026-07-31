package fleet

type fakeEndpointCapabilities struct{ backend *fakeBackend }

func (f fakeEndpointCapabilities) Create(req CreateRequest) (CreatedEndpoint, error) {
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
