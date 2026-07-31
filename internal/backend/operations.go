package backend

import "fmt"

type EndpointRef struct {
	Backend string
	Handle  string
}

type CreateEndpointRequest struct {
	PreferredBackend string
	FallbackBackend  string
	Container        string
	Name             string
	WorkingDirectory string
}

type CreateEndpointResult struct {
	Endpoint   EndpointRef
	IsFallback bool
}

type SubmitPromptRequest struct {
	Endpoint EndpointRef
	Prompt   string
}

type SubmitPromptResult struct {
	Status string
}

type Service struct {
	adapters AdapterRegistry
}

type Adapter interface {
	Create(container, name, workingDirectory string) (string, error)
	Submit(handle, prompt string) (SubmitPromptResult, error)
	Probe(handle string) (EndpointObservation, error)
	Dispose(handle string) error
}

type AdapterRegistry interface {
	Resolve(name string) (Adapter, error)
}

// NewService constructs the typed backend capability service. The registry
// returns typed capabilities, never session adapter objects or filesystem
// authority.
func NewService(registry AdapterRegistry) Service {
	return Service{adapters: registry}
}

func newService(registry AdapterRegistry) Service { return NewService(registry) }

func (s Service) CreateEndpoint(request CreateEndpointRequest) (CreateEndpointResult, error) {
	adapter, err := s.adapters.Resolve(request.PreferredBackend)
	resolved := request.PreferredBackend
	fallback := false
	if err != nil {
		if request.FallbackBackend == "" || request.FallbackBackend == request.PreferredBackend {
			return CreateEndpointResult{}, fmt.Errorf("resolving backend %q: %w", request.PreferredBackend, err)
		}
		adapter, err = s.adapters.Resolve(request.FallbackBackend)
		if err != nil {
			return CreateEndpointResult{}, fmt.Errorf("resolving fallback backend %q: %w", request.FallbackBackend, err)
		}
		resolved = request.FallbackBackend
		fallback = true
	}
	handle, err := adapter.Create(request.Container, request.Name, request.WorkingDirectory)
	if err != nil {
		return CreateEndpointResult{}, err
	}
	return CreateEndpointResult{Endpoint: EndpointRef{Backend: resolved, Handle: handle}, IsFallback: fallback}, nil
}

func (s Service) SubmitPrompt(request SubmitPromptRequest) (SubmitPromptResult, error) {
	adapter, err := s.adapters.Resolve(request.Endpoint.Backend)
	if err != nil {
		return SubmitPromptResult{}, fmt.Errorf("resolving bound backend %q: %w", request.Endpoint.Backend, err)
	}
	return adapter.Submit(request.Endpoint.Handle, request.Prompt)
}

func (s Service) ProbeEndpoint(endpoint EndpointRef) (EndpointObservation, error) {
	adapter, err := s.adapters.Resolve(endpoint.Backend)
	if err != nil {
		return EndpointObservation{State: EndpointUnresolved, Detail: fmt.Sprintf("resolving bound backend %q: %v", endpoint.Backend, err)}, nil
	}
	observation, err := adapter.Probe(endpoint.Handle)
	if err != nil {
		return ObservationFromProbeError(endpoint, err), nil
	}
	return observation, nil
}

func (s Service) DisposeEndpoint(endpoint EndpointRef) error {
	adapter, err := s.adapters.Resolve(endpoint.Backend)
	if err != nil {
		return fmt.Errorf("resolving bound backend %q: %w", endpoint.Backend, err)
	}
	return adapter.Dispose(endpoint.Handle)
}
