package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
)

type spawnSessionEndpoints struct {
	resolve func(string) (backend.Backend, string, error)
	bound   map[string]backend.Backend
}

func newSpawnSessionEndpoints() fleet.EndpointCapabilities {
	return &spawnSessionEndpoints{resolve: backend.Resolve, bound: map[string]backend.Backend{}}
}

// CreateReserved is the single mandatory reservation-aware create of the
// canonical launch path. It consumes the exact launch reservation identity
// (ReservationID/FenceToken) on EVERY call — first attempt AND recovery — and
// delegates to the real backend find-or-create contract, which is keyed by
// the launch intent's generation-scoped window label (the same reservation
// always resolves to the SAME window). A call without a reservation identity
// fails closed (no unreserved create). A backend without a real
// reservation-aware find-or-create contract fails closed BEFORE acquisition
// with a typed owner-clean error — never a silently fresh NewWindow, fallback
// backend, or replacement endpoint.
func (s *spawnSessionEndpoints) CreateReserved(req fleet.CreateRequest) (fleet.CreatedEndpoint, error) {
	if strings.TrimSpace(req.ReservationID) == "" || strings.TrimSpace(req.FenceToken) == "" {
		return fleet.CreatedEndpoint{}, fmt.Errorf("spawn endpoint create requires the exact launch reservation identity (reservation id + fence token); unreserved create is not allowed")
	}
	bk, name, err := s.resolve(req.PreferredBackend)
	if err != nil {
		return fleet.CreatedEndpoint{}, err
	}
	if hb, ok := bk.(*backend.HerdrBackend); ok {
		hb.Cwd = req.Cwd
	}
	handle, err := reservedWindow(bk, backend.WorkspaceTag(req.Home), req.TabName, name, req.ReservationID)
	if err != nil {
		return fleet.CreatedEndpoint{}, err
	}
	ep := fleet.CreatedEndpoint{Backend: name, Handle: handle, Metadata: map[string]string{}}
	if ex, ok := bk.(backend.BackendMetaExtras); ok {
		for k, v := range ex.MetaExtras() {
			ep.Metadata[k] = v
		}
		ep.SessionOwner = ep.Metadata["herdr_session"]
		ep.WorkspaceID = ep.Metadata["herdr_workspace_id"]
		ep.TabID = ep.Metadata["herdr_tab_id"]
	}
	s.bound[spawnEndpointKey(ep)] = bk
	return ep, nil
}

// reservedWindow invokes the real reservation-aware find-or-create contract
// of the selected backend. tmux and herdr implement FindOrCreateWindow
// (find-or-create under the reservation-derived generation-scoped window
// label; ambiguity fails closed). Any backend without that contract cannot
// prove reservation-owned recovery and fails closed BEFORE acquisition with a
// typed owner-clean error instead of allocating a replacement.
func reservedWindow(bk backend.Backend, session, name, backendName, reservationID string) (string, error) {
	f, ok := bk.(interface {
		FindOrCreateWindow(session, name string) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("backend %q has no reservation-aware find-or-create contract (FindOrCreateWindow); cannot recover launch reservation %q — fail closed before acquisition (no fresh NewWindow, no fallback backend, no replacement endpoint)", backendName, reservationID)
	}
	return f.FindOrCreateWindow(session, name)
}

func spawnEndpointKey(ep fleet.CreatedEndpoint) string { return ep.Backend + "\x00" + ep.Handle }

func (s *spawnSessionEndpoints) backend(ep fleet.CreatedEndpoint) (backend.Backend, error) {
	if strings.TrimSpace(ep.Backend) == "" || strings.TrimSpace(ep.Handle) == "" {
		return nil, fmt.Errorf("endpoint identity is incomplete")
	}

	herdrSession := ""
	if ep.Backend == "herdr" {
		var err error
		herdrSession, err = spawnEndpointHerdrSession(ep)
		if err != nil {
			return nil, err
		}
	}

	bk, bound := s.bound[spawnEndpointKey(ep)]
	if !bound {
		if s.resolve == nil {
			return nil, fmt.Errorf("endpoint %q on backend %q is not bound", ep.Handle, ep.Backend)
		}
		var name string
		var err error
		bk, name, err = s.resolve(ep.Backend)
		if err != nil {
			return nil, fmt.Errorf("resolving recorded endpoint backend %q: %w", ep.Backend, err)
		}
		if name != ep.Backend {
			return nil, fmt.Errorf("recorded endpoint backend mismatch: requested %q, resolved %q", ep.Backend, name)
		}
		if bk == nil {
			return nil, fmt.Errorf("resolving recorded endpoint backend %q returned no backend", ep.Backend)
		}
	}
	if hb, ok := bk.(*backend.HerdrBackend); ok {
		if bound {
			if hb.Session != herdrSession {
				return nil, fmt.Errorf("herdr session ownership mismatch: bound session %q does not match endpoint session %q", hb.Session, herdrSession)
			}
		} else {
			hb.Session = herdrSession
			hb.SeedTeardownTab(ep.TabID)
			hb.TeardownWorkspaceID = ep.WorkspaceID
		}
	}
	return bk, nil
}

func spawnEndpointHerdrSession(ep fleet.CreatedEndpoint) (string, error) {
	if strings.TrimSpace(ep.SessionOwner) != ep.SessionOwner {
		return "", fmt.Errorf("herdr endpoint %q has invalid session owner", ep.Handle)
	}
	// Herdr pane IDs can contain a colon too. Only the three-part form carries
	// an unambiguous session prefix; a shorter handle needs SessionOwner.
	var handleSession string
	if strings.Count(ep.Handle, ":") >= 2 {
		handleSession, _ = backend.ParseWindow(ep.Handle)
		if handleSession == "" {
			return "", fmt.Errorf("herdr endpoint %q has an invalid session prefix", ep.Handle)
		}
	}
	if ep.SessionOwner != "" && handleSession != "" && handleSession != ep.SessionOwner {
		return "", fmt.Errorf("herdr session ownership mismatch: endpoint session %q does not match handle session %q", ep.SessionOwner, handleSession)
	}
	if ep.SessionOwner != "" {
		return ep.SessionOwner, nil
	}
	if handleSession == "" {
		return "", fmt.Errorf("herdr endpoint %q has no durable session identity", ep.Handle)
	}
	return handleSession, nil
}

func (s *spawnSessionEndpoints) Submit(ep fleet.CreatedEndpoint, text string) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	return bk.SendKeys(ep.Handle, text)
}

func (s *spawnSessionEndpoints) Probe(ep fleet.CreatedEndpoint) (fleet.SpawnEndpointObservation, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return fleet.SpawnEndpointObservation{}, err
	}
	return backend.ObserveEndpoint(bk, ep.Handle, harnessProcessMatcher(ep.Harness)), nil
}

func (s *spawnSessionEndpoints) Capture(ep fleet.CreatedEndpoint, lines int) (string, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return "", err
	}
	return bk.Capture(ep.Handle, lines)
}

func (s *spawnSessionEndpoints) Dispose(ep fleet.CreatedEndpoint) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	if err := bk.Teardown(ep.Handle); err != nil {
		return err
	}
	delete(s.bound, spawnEndpointKey(ep))
	return nil
}

// harnessProcessMatcher resolves a task's harness into the process knowledge
// backend.ObserveEndpoint takes; an empty or unknown harness matches nothing.
func harnessProcessMatcher(name string) backend.ProcessMatcher {
	return func(process string) bool { return harness.ProcessMatches(name, process) }
}
