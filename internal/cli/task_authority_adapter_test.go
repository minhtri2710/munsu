package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

// TestTaskAuthorityComposition proves the command context exposes the
// concrete Authority composed over the filesystem Store adapter once per
// command context (ADR-0007 §9) without package globals. Store construction
// performs no migration and no mutation: composing the Authority must leave
// the resolved home untouched. A canonical read on a fresh home returns an
// empty committed view and creates only the shared state/.dispatch.lock
// artifact, never authority state.
func TestTaskAuthorityComposition(t *testing.T) {
	homeDir := t.TempDir()
	ctx := Ctx{Home: homeDir}

	auth, err := ctx.TaskAuthority()
	if err != nil {
		t.Fatalf("composing task authority: %v", err)
	}
	if auth == nil {
		t.Fatal("TaskAuthority() returned a nil Authority")
	}
	if again, err := ctx.TaskAuthority(); err != nil {
		t.Fatalf("second TaskAuthority() call: %v", err)
	} else if again != auth {
		t.Fatal("TaskAuthority() composed a new Authority on repeat; composition must happen once per command context")
	}

	assertEmptyHome(t, homeDir, "after composition")

	tasks, err := auth.List()
	if err != nil {
		t.Fatalf("Authority.List on a fresh home: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("fresh home should list no tasks, got %d", len(tasks))
	}
	assertOnlyLockArtifact(t, homeDir)
}

// TestTaskAuthorityInjection proves tests can inject an Authority backed by
// an in-memory Store into the command context. The injected Authority is
// returned unchanged and remains fully functional; the filesystem Store
// adapter is never constructed for the context.
func TestTaskAuthorityInjection(t *testing.T) {
	homeDir := t.TempDir()
	injected := taskauthority.New(taskauthority.NewMemStore())
	ctx := Ctx{Home: homeDir, taskAuthority: injected}

	auth, err := ctx.TaskAuthority()
	if err != nil {
		t.Fatalf("TaskAuthority() with injected Authority: %v", err)
	}
	if auth != injected {
		t.Fatal("TaskAuthority() did not return the injected Authority")
	}

	res, err := injected.Create(taskauthority.CreateRequest{
		OperationID: "cli-inject-op-1",
		Actor:       taskauthority.Actor{ID: "cli-test", Rank: "soldier"},
		TaskID:      "ship-injected",
		Owner:       "cli-test",
		Description: "task created through the injected in-memory Authority",
		Kind:        "ship",
	})
	if err != nil {
		t.Fatalf("injected Authority Create: %v", err)
	}
	if res.Phase != taskauthority.PhaseQueued || res.Generation != 1 {
		t.Fatalf("injected Authority Create result = %+v", res)
	}
	agg, err := injected.Get("ship-injected")
	if err != nil {
		t.Fatalf("injected Authority Get: %v", err)
	}
	if agg.Definition.Owner != "cli-test" || agg.Definition.Description != "task created through the injected in-memory Authority" {
		t.Fatalf("injected Authority aggregate = %+v", agg)
	}

	assertEmptyHome(t, homeDir, "with injected Authority")
}

// TestWithHomeDoesNotComposeTaskAuthority proves commands that do not need
// Task Authority receive no pass-through construction: a withHome handler
// that never requests the Authority leaves the resolved home untouched.
func TestWithHomeDoesNotComposeTaskAuthority(t *testing.T) {
	homeDir := t.TempDir()
	prev := homeOverride
	homeOverride = homeDir
	defer func() { homeOverride = prev }()

	handled := false
	fn := withHome(func(_ *cobra.Command, _ []string, _ Ctx) error {
		handled = true
		return nil
	})
	if err := fn(&cobra.Command{}, nil); err != nil {
		t.Fatalf("withHome handler: %v", err)
	}
	if !handled {
		t.Fatal("withHome did not invoke the handler")
	}
	assertEmptyHome(t, homeDir, "after non-authority command")
}

// assertEmptyHome fails when homeDir contains any entry at all.
func assertEmptyHome(t *testing.T, homeDir, stage string) {
	t.Helper()
	entries, err := os.ReadDir(homeDir)
	if err != nil {
		t.Fatalf("reading home at %s: %v", stage, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("home not empty %s: %v", stage, names)
	}
}

// assertOnlyLockArtifact fails when the home contains anything beyond the
// shared state/.dispatch.lock artifact a canonical read may create.
func assertOnlyLockArtifact(t *testing.T, homeDir string) {
	t.Helper()
	entries, err := os.ReadDir(homeDir)
	if err != nil {
		t.Fatalf("reading home: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("canonical read must create only state/, got %v", names)
	}
	stateEntries, err := os.ReadDir(filepath.Join(homeDir, "state"))
	if err != nil {
		t.Fatalf("reading state/: %v", err)
	}
	if len(stateEntries) != 1 || stateEntries[0].Name() != ".dispatch.lock" {
		names := make([]string, 0, len(stateEntries))
		for _, e := range stateEntries {
			names = append(names, e.Name())
		}
		t.Fatalf("canonical read must create only state/.dispatch.lock, got %v", names)
	}
}
