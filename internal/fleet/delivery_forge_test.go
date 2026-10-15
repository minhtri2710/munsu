//go:build integration

package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestForgeRefusals is the table of the captured-forge refusals: a block row
// proves one refusal names its own reason, and the allow rows are the nearest
// valid cases. The Ready probe is one rule shared by every path that delivers
// through the forge, so its block row runs each of them.
func TestForgeRefusals(t *testing.T) {
	ghIdent := domain.DeliveryIdentity{Provider: "github"}
	notReady := func(t *testing.T) { t.Setenv("PATH", t.TempDir()) }
	ready := func(t *testing.T) { installFakeGH(t) }
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		calls []func() error
		want  string // empty means every call is accepted
	}{
		{
			name:  "a forge whose probe is not Ready blocks every forge path",
			setup: notReady,
			calls: []func() error{
				func() error { _, err := forgeClientFor(githubForgeStep); return err },
				func() error { _, err := Preflight("direct-PR", "", githubForgeStep); return err },
			},
			want: "forge adapter github probe absent: configured forge is not Ready",
		},
		{
			name: "a baseline task has no forge and blocks",
			calls: []func() error{
				func() error {
					_, err := forgeClientFor(taskauthority.DeliveryStep{Baseline: true, ProbeState: "baseline"})
					return err
				},
			},
			want: "no configured forge tool",
		},
		{
			name: "an identity of another provider than the step's adapter blocks",
			calls: []func() error{
				func() error { _, err := forgeClientForIdentity(gitlabForgeStep, ghIdent); return err },
			},
			want: "does not match the task's configured forge adapter",
		},
		{
			name:  "a Ready github forge routes to its client on every forge path",
			setup: ready,
			calls: []func() error{
				func() error {
					forge, err := forgeClientFor(githubForgeStep)
					if err == nil && forge.github == nil {
						return errors.New("forgeClientFor returned no gh-axi client")
					}
					return err
				},
				func() error { _, err := Preflight("direct-PR", "", githubForgeStep); return err },
			},
		},
		{
			name:  "an identity of the step's own provider resolves",
			setup: ready,
			calls: []func() error{
				func() error { _, err := forgeClientForIdentity(githubForgeStep, ghIdent); return err },
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			for i, call := range tc.calls {
				err := call()
				if tc.want == "" && err != nil {
					t.Fatalf("call %d err = %v, want accepted", i, err)
				}
				if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
					t.Fatalf("call %d err = %v, want %q", i, err, tc.want)
				}
			}
		})
	}
}
