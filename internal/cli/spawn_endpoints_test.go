package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type spawnEndpointBackend struct {
	window            string
	submitted         []string
	metadata          map[string]string
	findOrCreateCalls int
}

func (b *spawnEndpointBackend) NewWindow(string, string) (string, error) { return b.window, nil }
func (b *spawnEndpointBackend) FindOrCreateWindow(string, string) (string, error) {
	b.findOrCreateCalls++
	return b.window, nil
}
func (b *spawnEndpointBackend) SendKeys(_ string, text string) error {
	b.submitted = append(b.submitted, text)
	return nil
}
func (*spawnEndpointBackend) Capture(string, int) (string, error) { return "ready", nil }
func (*spawnEndpointBackend) Alive(string) bool                   { return true }
func (*spawnEndpointBackend) Teardown(string) error               { return nil }
func (b *spawnEndpointBackend) MetaExtras() map[string]string     { return b.metadata }

type spawnLabelBackend struct {
	window    string
	gotLabel  string
	submitted []string
}

func (b *spawnLabelBackend) NewWindow(string, string) (string, error) { return b.window, nil }
func (b *spawnLabelBackend) FindOrCreateWindow(label, _ string) (string, error) {
	b.gotLabel = label
	return b.window, nil
}
func (b *spawnLabelBackend) SendKeys(_ string, text string) error {
	b.submitted = append(b.submitted, text)
	return nil
}
func (*spawnLabelBackend) Capture(string, int) (string, error) { return "ready", nil }
func (*spawnLabelBackend) Alive(string) bool                   { return true }
func (*spawnLabelBackend) Teardown(string) error               { return nil }

// unsupportedSpawnBackend implements backend.Backend WITHOUT the real
// reservation-aware find-or-create contract (FindOrCreateWindow) and WITHOUT
// MetaExtras, so the factory must fail closed before acquisition.
type unsupportedSpawnBackend struct{ window string }

func (b *unsupportedSpawnBackend) NewWindow(string, string) (string, error) { return b.window, nil }
func (*unsupportedSpawnBackend) SendKeys(string, string) error              { return nil }
func (*unsupportedSpawnBackend) Capture(string, int) (string, error)        { return "ready", nil }
func (*unsupportedSpawnBackend) Alive(string) bool                          { return true }
func (*unsupportedSpawnBackend) Teardown(string) error                      { return nil }

func TestSpawnSessionEndpointsDerivesWorkspaceLabel(t *testing.T) {
	bk := &spawnLabelBackend{window: "window-1"}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (backend.Backend, string, error) { return bk, "tmux", nil },
		bound:   map[string]backend.Backend{},
	}
	homeDir := t.TempDir()
	if _, err := endpoints.CreateReserved(fleet.CreateRequest{Home: homeDir, ReservationID: "epres-x", FenceToken: "epfence-x"}); err != nil {
		t.Fatal(err)
	}
	if want := backend.WorkspaceTag(homeDir); bk.gotLabel != want {
		t.Fatalf("label=%q want %q", bk.gotLabel, want)
	}
	if bk.gotLabel == "" {
		t.Fatal("label must not be empty")
	}
}

func TestSpawnSessionEndpointsPreservesBackendIdentityAndMetadata(t *testing.T) {
	bk := &spawnEndpointBackend{window: "window-1", metadata: map[string]string{
		"herdr_session":      "session-1",
		"herdr_workspace_id": "workspace-1",
		"herdr_tab_id":       "tab-1",
	}}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (backend.Backend, string, error) { return bk, "herdr", nil },
		bound:   map[string]backend.Backend{},
	}
	created, err := endpoints.CreateReserved(fleet.CreateRequest{ReservationID: "epres-x", FenceToken: "epfence-x"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Backend != "herdr" || created.SessionOwner != "session-1" || created.WorkspaceID != "workspace-1" || created.TabID != "tab-1" {
		t.Fatalf("created endpoint lost backend metadata: %+v", created)
	}
	if err := endpoints.Submit(created, "bound"); err != nil {
		t.Fatal(err)
	}
	if len(bk.submitted) != 1 || bk.submitted[0] != "bound" {
		t.Fatalf("created endpoint not bound to resolver backend: %v", bk.submitted)
	}
}

func TestSpawnSessionEndpointsKeepsCreatorBindingsSeparate(t *testing.T) {
	first := &spawnEndpointBackend{window: "window-1"}
	second := &spawnEndpointBackend{window: "window-2"}
	resolved := []backend.Backend{first, second}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (backend.Backend, string, error) {
			bk := resolved[0]
			resolved = resolved[1:]
			return bk, "tmux", nil
		},
		bound: map[string]backend.Backend{},
	}
	firstEndpoint, err := endpoints.CreateReserved(fleet.CreateRequest{ReservationID: "epres-1", FenceToken: "epfence-1"})
	if err != nil {
		t.Fatal(err)
	}
	secondEndpoint, err := endpoints.CreateReserved(fleet.CreateRequest{ReservationID: "epres-2", FenceToken: "epfence-2"})
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

// TestSpawnSessionEndpointsRequiresReservationIdentity proves the factory has
// no unreserved create path: CreateReserved without the exact launch
// reservation identity fails closed before any adapter call.
func TestSpawnSessionEndpointsRequiresReservationIdentity(t *testing.T) {
	called := false
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (backend.Backend, string, error) {
			called = true
			return &spawnLabelBackend{window: "window-1"}, "tmux", nil
		},
		bound: map[string]backend.Backend{},
	}
	if _, err := endpoints.CreateReserved(fleet.CreateRequest{}); err == nil {
		t.Fatal("unreserved create must fail closed")
	} else if !strings.Contains(err.Error(), "reservation identity") {
		t.Fatalf("error = %v, want reservation-identity fail-closed", err)
	}
	if called {
		t.Fatal("adapter must not be called for an unreserved create")
	}
}

// TestSpawnSessionEndpointsFindOrCreateSameReservationSameEndpoint proves the
// production factory delegates to the real find-or-create contract: the same
// reservation (same generation-scoped window label) returns the SAME endpoint
// on recovery — never a replacement.
func TestSpawnSessionEndpointsFindOrCreateSameReservationSameEndpoint(t *testing.T) {
	bk := &spawnEndpointBackend{window: "window-1"}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (backend.Backend, string, error) { return bk, "tmux", nil },
		bound:   map[string]backend.Backend{},
	}
	req := fleet.CreateRequest{Home: t.TempDir(), TabName: "mu-proj-task-g1", ReservationID: "epres-x", FenceToken: "epfence-x"}
	first, err := endpoints.CreateReserved(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := endpoints.CreateReserved(req)
	if err != nil {
		t.Fatalf("find-or-create re-entry: %v", err)
	}
	if first.Handle != second.Handle || first.Handle != "window-1" {
		t.Fatalf("same reservation must return the same endpoint: %+v vs %+v", first, second)
	}
	if bk.findOrCreateCalls != 2 {
		t.Fatalf("find-or-create contract calls = %d, want 2 (first attempt AND recovery)", bk.findOrCreateCalls)
	}
}

// TestSpawnSessionEndpointsUnsupportedBackendFailsClosed proves a backend
// without the real reservation-aware find-or-create contract fails closed
// BEFORE acquisition with a typed owner-clean error — never a silently fresh
// NewWindow, fallback backend, or replacement endpoint.
func TestSpawnSessionEndpointsUnsupportedBackendFailsClosed(t *testing.T) {
	bk := &unsupportedSpawnBackend{}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string, string) (backend.Backend, string, error) { return bk, "orca", nil },
		bound:   map[string]backend.Backend{},
	}
	_, err := endpoints.CreateReserved(fleet.CreateRequest{ReservationID: "epres-x", FenceToken: "epfence-x"})
	if err == nil {
		t.Fatal("unsupported backend must fail closed before acquisition")
	}
	if !strings.Contains(err.Error(), "no reservation-aware find-or-create") || !strings.Contains(err.Error(), "fail closed") {
		t.Fatalf("error = %v, want typed fail-closed before acquisition", err)
	}
}
