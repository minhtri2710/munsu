package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// TestBoundEndpointOwnership is the one table for the rule "every munsu
// command that acts on a bound pane refuses when the task does not record the
// pane's backend", and for the ownership checks that ride with it. Each adapter
// reaches boundBackend through its own call site, so each is a column.
func TestBoundEndpointOwnership(t *testing.T) {
	type resolver = func(string, map[string]string) (backend.Backend, string, error)
	adapters := []struct {
		name string
		call func(resolve resolver, meta map[string]string) error
	}{
		{"capture", func(r resolver, meta map[string]string) error {
			_, err := sessionBoundCapture{resolve: r}.Capture("/home", meta, 1)
			return err
		}},
		{"captain probe", func(r resolver, meta map[string]string) error {
			_, err := sessionProbeEndpoint{resolve: r}.Probe("/home", meta)
			return err
		}},
		{"captain nudge", func(r resolver, meta map[string]string) error {
			_, err := sessionNudgeEndpoint{resolve: r}.Nudge("/home", meta, "payload")
			return err
		}},
		{"captain retire", func(r resolver, meta map[string]string) error {
			return sessionRetireEndpoint{resolve: r}.Retire("/home", meta)
		}},
		{"mailbox sender", func(r resolver, meta map[string]string) error {
			_, err := sessionMailboxSender{resolve: r}.backend("/home", meta)
			return err
		}},
		{"soldier endpoints", func(r resolver, meta map[string]string) error {
			_, err := sessionSoldierEndpoints{resolve: r}.backend("/home", meta)
			return err
		}},
		{"bound teardown", func(r resolver, meta map[string]string) error {
			_, _, err := sessionBoundTeardown{resolve: r}.resolveBound("/home", meta)
			return err
		}},
	}
	rows := []struct {
		name         string
		mutate       func(meta map[string]string)
		resolved     string
		want         string
		wantResolves int
	}{
		{"backend not recorded", func(m map[string]string) { delete(m, "backend") }, "herdr", "task does not record the pane's backend", 0},
		{"recorded backend differs from the resolved one", func(m map[string]string) { m["backend"] = "tmux" }, "herdr", `bound backend resolved as "herdr", task records "tmux"`, 1},
		{"handle in a foreign herdr session", func(m map[string]string) { m["window"] = "other-session:w1:p1" }, "herdr", "herdr session ownership mismatch", 1},
		{"bare pane id names no session", func(m map[string]string) { m["window"] = "w1:p1" }, "herdr", "", 1},
		{"matching backend and session", func(map[string]string) {}, "herdr", "", 1},
	}
	for _, a := range adapters {
		for _, row := range rows {
			t.Run(a.name+"/"+row.name, func(t *testing.T) {
				resolves := 0
				meta := boundMeta()
				row.mutate(meta)
				err := a.call(func(string, map[string]string) (backend.Backend, string, error) {
					resolves++
					return &guardBackend{}, row.resolved, nil
				}, meta)
				if row.want == "" {
					if err != nil {
						t.Fatalf("%s with a matching backend and session = %v, want acceptance", a.name, err)
					}
				} else {
					wantErrContains(t, err, row.want, a.name+" "+row.name)
				}
				if resolves != row.wantResolves {
					t.Fatalf("%s resolve calls = %d, want %d", a.name, resolves, row.wantResolves)
				}
			})
		}
	}
}

// TestSpawnEndpointOwnership is the same group for the endpoints a spawn
// records on fleet.CreatedEndpoint rather than on task meta.
func TestSpawnEndpointOwnership(t *testing.T) {
	foreign := fleet.CreatedEndpoint{Backend: "herdr", Handle: "actual:w3A:p2", SessionOwner: "recorded"}
	owned := fleet.CreatedEndpoint{Backend: "herdr", Handle: "recorded:w3A:p2", SessionOwner: "recorded"}
	for _, tc := range []struct {
		name         string
		ep           fleet.CreatedEndpoint
		boundTo      string
		resolved     string
		want         string
		wantResolves int
	}{
		{"handle session differs from the recorded owner", foreign, "", "herdr", `endpoint session "recorded" does not match handle session "actual"`, 0},
		{"handle session equals the recorded owner", owned, "", "herdr", "", 1},
		{"bound adapter serves another session", owned, "different", "", `bound session "different" does not match endpoint session "recorded"`, 0},
		{"bound adapter serves the recorded session", owned, "recorded", "", "", 0},
		{"recorded backend differs from the resolved one", owned, "", "tmux", `recorded endpoint backend mismatch: requested "herdr", resolved "tmux"`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolves := 0
			endpoints := &spawnSessionEndpoints{
				resolve: func(string) (backend.Backend, string, error) {
					resolves++
					return &spawnEndpointBackend{}, tc.resolved, nil
				},
				bound: map[string]backend.Backend{},
			}
			if tc.boundTo != "" {
				endpoints.bound[spawnEndpointKey(tc.ep)] = &backend.HerdrBackend{Session: tc.boundTo}
			}
			_, err := endpoints.backend(tc.ep)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("backend(%+v) = %v, want acceptance", tc.ep, err)
				}
			} else {
				wantErrContains(t, err, tc.want, "backend for "+tc.name)
			}
			if resolves != tc.wantResolves {
				t.Fatalf("resolve calls = %d, want %d", resolves, tc.wantResolves)
			}
		})
	}
}
