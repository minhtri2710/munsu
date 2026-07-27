package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/spawn"
)

type spawnEndpointBackend struct {
	window    string
	submitted []string
	metadata  map[string]string
}

func (b *spawnEndpointBackend) NewWindow(string, string) (string, error) { return b.window, nil }
func (b *spawnEndpointBackend) SendKeys(_ string, text string) error {
	b.submitted = append(b.submitted, text)
	return nil
}
func (*spawnEndpointBackend) Capture(string, int) (string, error) { return "ready", nil }
func (*spawnEndpointBackend) Alive(string) bool                   { return true }
func (*spawnEndpointBackend) Teardown(string) error               { return nil }
func (b *spawnEndpointBackend) MetaExtras() map[string]string     { return b.metadata }

func TestSpawnSessionEndpointsPreservesBackendIdentityAndMetadata(t *testing.T) {
	backend := &spawnEndpointBackend{window: "window-1", metadata: map[string]string{
		"herdr_session":      "session-1",
		"herdr_workspace_id": "workspace-1",
		"herdr_tab_id":       "tab-1",
	}}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (session.Backend, string, error) { return backend, "herdr", nil },
		bound:   map[string]session.Backend{},
	}
	created, err := endpoints.Create(spawn.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if created.Backend != "herdr" || created.SessionOwner != "session-1" || created.WorkspaceID != "workspace-1" || created.TabID != "tab-1" {
		t.Fatalf("created endpoint lost backend metadata: %+v", created)
	}
	if err := endpoints.Submit(created, "bound"); err != nil {
		t.Fatal(err)
	}
	if len(backend.submitted) != 1 || backend.submitted[0] != "bound" {
		t.Fatalf("created endpoint not bound to resolver backend: %v", backend.submitted)
	}
}

func TestSpawnSessionEndpointsKeepsCreatorBindingsSeparate(t *testing.T) {
	first := &spawnEndpointBackend{window: "window-1"}
	second := &spawnEndpointBackend{window: "window-2"}
	resolved := []session.Backend{first, second}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (session.Backend, string, error) {
			backend := resolved[0]
			resolved = resolved[1:]
			return backend, "tmux", nil
		},
		bound: map[string]session.Backend{},
	}
	firstEndpoint, err := endpoints.Create(spawn.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	secondEndpoint, err := endpoints.Create(spawn.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := endpoints.Submit(firstEndpoint, "first"); err != nil {
		t.Fatal(err)
	}
	if err := endpoints.Submit(secondEndpoint, "second"); err != nil {
		t.Fatal(err)
	}
	if len(first.submitted) != 1 || first.submitted[0] != "first" {
		t.Fatalf("first endpoint crossed backend binding: %v", first.submitted)
	}
	if len(second.submitted) != 1 || second.submitted[0] != "second" {
		t.Fatalf("second endpoint crossed backend binding: %v", second.submitted)
	}
}
