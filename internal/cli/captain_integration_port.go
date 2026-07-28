package cli

import (
	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
	"os"
	"path/filepath"
	"strings"
)

type captainIntegrationAdapter struct{}

func (captainIntegrationAdapter) EnsureCaptain(h string) error {
	a := &bootstrap.PiAdapter{HomeDir: h, Cwd: h, Scope: string(bootstrap.ScopeProject)}
	target, _, _, e := a.InstallPiExtension()
	if e != nil {
		m := e.Error()
		if strings.Contains(m, "pi not found") || strings.Contains(m, "munsu not found") {
			return nil
		}
		return e
	}
	b, e := os.ReadFile(target)
	if e != nil {
		return e
	}
	d := filepath.Join(h, ".pi", "extensions")
	if e = os.MkdirAll(d, 0755); e != nil {
		return e
	}
	for _, n := range []string{"munsu-captain-turnend-guard.ts", "munsu-captain-pi-watch.ts"} {
		if e = os.WriteFile(filepath.Join(d, n), b, 0644); e != nil {
			return e
		}
	}
	return nil
}
func (captainIntegrationAdapter) Status(h, n string) (fleet.IntegrationStatus, error) {
	r, e := bootstrap.Status(h, h, n, bootstrap.ScopeProject)
	if e != nil {
		return fleet.IntegrationStatus{}, e
	}
	return fleet.IntegrationStatus{Harness: r.Harness, Scope: string(r.Scope), State: r.State, Message: r.Message}, nil
}
