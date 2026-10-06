package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

func TestSpawnSessionEndpointsRecordedEndpointFreshSession(t *testing.T) {
	bk := &spawnEndpointBackend{window: "default:w3A:p2"}
	endpoints := &spawnSessionEndpoints{
		resolve: func(string) (backend.Backend, string, error) { return bk, "herdr", nil },
		bound:   map[string]backend.Backend{},
	}
	_, err := endpoints.Probe(fleet.CreatedEndpoint{Backend: "herdr", Handle: "default:w3A:p2"})
	if err != nil && strings.Contains(err.Error(), "is not bound") {
		t.Fatalf("recorded endpoint cannot reach backend observation in a fresh CLI session: %v", err)
	}
	if bk.findOrCreateCalls != 0 {
		t.Fatal("observing a recorded endpoint must not create a replacement")
	}
}
