package backend

import (
	"errors"
	"testing"
)

type fakeAdapterResolver struct {
	adapters map[string]Adapter
	errors   map[string]error
}

func (r fakeAdapterResolver) Resolve(name string) (Adapter, error) {
	if err := r.errors[name]; err != nil {
		return nil, err
	}
	adapter := r.adapters[name]
	if adapter == nil {
		return nil, errors.New("unavailable")
	}
	return adapter, nil
}

type fakeEndpointAdapter struct {
	created  string
	submits  []string
	disposed string
}

func (a *fakeEndpointAdapter) Create(_, name, _ string) (string, error) {
	a.created = name
	return "endpoint", nil
}
func (a *fakeEndpointAdapter) Submit(handle, prompt string) (SubmitPromptResult, error) {
	a.submits = append(a.submits, handle+":"+prompt)
	return SubmitPromptResult{Status: "submitted"}, nil
}
func (*fakeEndpointAdapter) Probe(string) (EndpointStatus, error) {
	return EndpointStatus{Alive: true, RecognizedAgent: true}, nil
}
func (a *fakeEndpointAdapter) Dispose(handle string) error { a.disposed = handle; return nil }

func TestCreateEndpointMayFallbackOnlyForNewEndpoint(t *testing.T) {
	tmux := &fakeEndpointAdapter{}
	service := NewService(fakeAdapterResolver{adapters: map[string]Adapter{"tmux": tmux}, errors: map[string]error{"herdr": errors.New("down")}})
	result, err := service.CreateEndpoint(CreateEndpointRequest{PreferredBackend: "herdr", FallbackBackend: "tmux", Name: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Endpoint.Backend != "tmux" || !result.IsFallback {
		t.Fatalf("result = %+v", result)
	}
}

func TestBoundOperationsFailClosedWithoutFallback(t *testing.T) {
	service := NewService(fakeAdapterResolver{adapters: map[string]Adapter{}, errors: map[string]error{"herdr": errors.New("down")}})
	if _, err := service.SubmitPrompt(SubmitPromptRequest{Endpoint: EndpointRef{Backend: "herdr", Handle: "p1"}, Prompt: "hello"}); err == nil {
		t.Fatal("SubmitPrompt fell back")
	}
	if _, err := service.ProbeEndpoint(EndpointRef{Backend: "herdr", Handle: "p1"}); err == nil {
		t.Fatal("ProbeEndpoint fell back")
	}
	if err := service.DisposeEndpoint(EndpointRef{Backend: "herdr", Handle: "p1"}); err == nil {
		t.Fatal("DisposeEndpoint fell back")
	}
}

func TestBoundOperationsUseResolvedAdapter(t *testing.T) {
	adapter := &fakeEndpointAdapter{}
	service := NewService(fakeAdapterResolver{adapters: map[string]Adapter{"tmux": adapter}})
	if got, err := service.SubmitPrompt(SubmitPromptRequest{Endpoint: EndpointRef{Backend: "tmux", Handle: "p1"}, Prompt: "hello"}); err != nil || got.Status != "submitted" {
		t.Fatalf("submit = %+v, %v", got, err)
	}
	if status, err := service.ProbeEndpoint(EndpointRef{Backend: "tmux", Handle: "p1"}); err != nil || !status.Alive {
		t.Fatalf("probe = %+v, %v", status, err)
	}
	if err := service.DisposeEndpoint(EndpointRef{Backend: "tmux", Handle: "p1"}); err != nil {
		t.Fatal(err)
	}
}
