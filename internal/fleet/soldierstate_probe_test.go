//go:build integration

package fleet

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type stateProbe struct {
	alive bool
	err   error
	ep    *taskauthority.EndpointBinding
}

func (p *stateProbe) Probe(_ string, ep taskauthority.EndpointBinding, _ string) (bool, error) {
	p.ep = &ep
	return p.alive, p.err
}

func TestReadWithProbeProbesTheCanonicalBinding(t *testing.T) {
	h := t.TempDir()
	mergeTestAuth(t, h, "task")
	probe := &stateProbe{alive: true}
	state, err := ReadWithProbe(h, "task", probe)
	if err != nil {
		t.Fatal(err)
	}
	if !state.PaneAlive {
		t.Fatal("expected alive")
	}
	if probe.ep == nil || probe.ep.Handle != "@1" || probe.ep.SessionOwner != "session-task" || probe.ep.WorkspaceID != "ws-task" || probe.ep.TabID != "tab-task" {
		t.Fatalf("probed endpoint = %+v, want the canonical binding", probe.ep)
	}
}

// TestReadWithProbeProbesUnboundAcquiredEndpoint probes the pane a launch
// acquired but never bound, until the task retires.
func TestReadWithProbeProbesUnboundAcquiredEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		retire bool
	}{
		{name: "queued unbound launch is probed"},
		{name: "retired unbound launch is not probed", retire: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchFixture(t, "unbound-probe")
			if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
				t.Fatalf("launch phases = %v, want simulated pre-projection crash", err)
			}
			acquired := f.aggregate().AcquiredEndpoint
			if acquired == nil {
				t.Fatal("fixture holds no acquired endpoint")
			}
			if tc.retire {
				RetireTask(Options{HomeDir: f.homeDir, ID: f.taskID, Force: true}, &recordingTeardown{alive: true}, fakeRetirementJournals{}, f.auth)
				if agg := f.aggregate(); agg.Phase != taskauthority.PhaseRetired || agg.AcquiredEndpoint != nil || agg.Retirement == nil || agg.Retirement.Acquired == nil {
					t.Fatalf("fixture phase %q acquired %v retirement %+v, want retired with the acquired endpoint released into retirement evidence", agg.Phase, agg.AcquiredEndpoint, agg.Retirement)
				}
			}
			probe := &stateProbe{alive: true}
			state, err := ReadWithProbe(f.homeDir, f.taskID, probe)
			if err != nil {
				t.Fatal(err)
			}
			if tc.retire {
				if probe.ep != nil || state.PaneAlive {
					t.Fatalf("retired task probed: pane_alive=%v endpoint=%+v", state.PaneAlive, probe.ep)
				}
				return
			}
			if !state.PaneAlive || probe.ep == nil || probe.ep.Handle != acquired.Handle || probe.ep.Backend != acquired.Backend {
				t.Fatalf("pane_alive=%v probed endpoint=%+v, want the acquired endpoint %+v", state.PaneAlive, probe.ep, acquired)
			}
		})
	}
}

func TestReadWithProbeFailsClosedOnProbeError(t *testing.T) {
	h := t.TempDir()
	mergeTestAuth(t, h, "task")
	state, err := ReadWithProbe(h, "task", &stateProbe{alive: true, err: errors.New("probe failed")})
	if err != nil {
		t.Fatal(err)
	}
	if state.PaneAlive {
		t.Fatal("probe error must fail closed")
	}
}
