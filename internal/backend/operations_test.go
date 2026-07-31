package backend

import (
	"errors"
	"testing"
)

type fakeAdapterResolver struct {
	adapters map[string]Adapter
	errors   map[string]error
	calls    *[]string
}

func (r fakeAdapterResolver) Resolve(name string) (Adapter, error) {
	if r.calls != nil {
		*r.calls = append(*r.calls, name)
	}
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
func (*fakeEndpointAdapter) Probe(string) (EndpointObservation, error) {
	return EndpointObservation{State: EndpointAlive, RecognizedAgent: true}, nil
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
	if observation, err := service.ProbeEndpoint(EndpointRef{Backend: "herdr", Handle: "p1"}); err != nil || observation.State != EndpointUnresolved {
		t.Fatalf("ProbeEndpoint = %+v, %v; want unresolved without fallback", observation, err)
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
	if status, err := service.ProbeEndpoint(EndpointRef{Backend: "tmux", Handle: "p1"}); err != nil || !status.Alive() {
		t.Fatalf("probe = %+v, %v", status, err)
	}
	if err := service.DisposeEndpoint(EndpointRef{Backend: "tmux", Handle: "p1"}); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointObservationStatesAreClosedVocabulary(t *testing.T) {
	want := []EndpointObservationState{
		EndpointAlive,
		EndpointStarting,
		EndpointUnresponsive,
		EndpointDead,
		EndpointUnknown,
		EndpointStaleIdentity,
		EndpointUnresolved,
	}
	for _, state := range want {
		if state.String() == "" {
			t.Fatalf("state %q has empty string form", state)
		}
		if !state.Valid() {
			t.Fatalf("state %q should be valid", state)
		}
	}
	if EndpointObservationInvalid.Valid() {
		t.Fatal("invalid zero state should not be valid")
	}
}

func TestProbeEndpointUnresolvedBackendIsTypedObservationWithoutFallback(t *testing.T) {
	tmux := &fakeEndpointAdapter{}
	var calls []string
	service := NewService(fakeAdapterResolver{adapters: map[string]Adapter{"tmux": tmux}, errors: map[string]error{"herdr": errors.New("down")}, calls: &calls})
	observation, err := service.ProbeEndpoint(EndpointRef{Backend: "herdr", Handle: "p1"})
	if err != nil {
		t.Fatalf("ProbeEndpoint should return typed unresolved observation, got error: %v", err)
	}
	if observation.State != EndpointUnresolved {
		t.Fatalf("state = %v, want unresolved", observation.State)
	}
	if tmux.created != "" {
		t.Fatalf("fallback adapter was touched: %+v", tmux)
	}
	if len(calls) != 1 || calls[0] != "herdr" {
		t.Fatalf("Resolve calls = %v, want only bound backend", calls)
	}
}

func TestProbeFailuresNeverBecomeDead(t *testing.T) {
	observation := ObservationFromProbeError(EndpointRef{Backend: "tmux", Handle: "p1"}, errors.New("timeout"))
	if observation.State == EndpointDead {
		t.Fatalf("probe failure mapped to dead: %+v", observation)
	}
	if observation.State != EndpointUnresponsive {
		t.Fatalf("state = %v, want unresponsive", observation.State)
	}
}
