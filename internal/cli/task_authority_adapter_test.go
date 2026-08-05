package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

// TestTaskAuthorityComposition proves the command context exposes the
// concrete canonical Authority composed over the opened owning Home once per
// command context without package globals. Composition opens the Home through
// the real home API and performs no mutation: composing the Authority and a
// canonical read on a fresh initialized Home must leave no task-authority
// state behind.
func TestTaskAuthorityComposition(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
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

	assertNoTaskAuthorityState(t, homeDir, "after composition")

	tasks, err := auth.List()
	if err != nil {
		t.Fatalf("Authority.List on a fresh home: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("fresh home should list no tasks, got %d", len(tasks))
	}
	assertNoTaskAuthorityState(t, homeDir, "after canonical read on a fresh home")
}

// TestTaskAuthorityFailsClosedOnUninitializedHome proves ordinary authority
// reads fail closed for an uninitialized Home rather than silently
// initializing state: only command semantics that explicitly create a Home
// initialize one.
func TestTaskAuthorityFailsClosedOnUninitializedHome(t *testing.T) {
	homeDir := t.TempDir()
	ctx := Ctx{Home: homeDir}

	if auth, err := ctx.TaskAuthority(); err == nil {
		t.Fatalf("TaskAuthority() on uninitialized home succeeded: %v", auth)
	}
	if _, err := ctx.TaskAuthorityFor(homeDir); err == nil {
		t.Fatal("TaskAuthorityFor() on uninitialized home succeeded")
	}
	entries, err := os.ReadDir(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("fail-closed composition initialized the home: %v", names)
	}
}

// TestTaskAuthorityInjection proves tests can inject a canonical Authority
// over an initialized Home into the command context. The injected Authority
// is returned unchanged and remains fully functional; no second composition
// happens for the context.
func TestTaskAuthorityInjection(t *testing.T) {
	homeDir := t.TempDir()
	h, err := home.Init(homeDir)
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	injected, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	ctx := Ctx{Home: homeDir, taskAuthority: injected}

	auth, err := ctx.TaskAuthority()
	if err != nil {
		t.Fatalf("TaskAuthority() with injected Authority: %v", err)
	}
	if auth != injected {
		t.Fatal("TaskAuthority() did not return the injected Authority")
	}

	tid, err := domain.NewTaskID("ship-injected")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      injected.HomeID(),
		TaskID:      tid,
		Owner:       "cli-test",
		Description: "task created through the injected canonical Authority",
		Kind:        "ship",
		Reason:      "test",
	}
	opID, err := domain.NewOperationID("cli-inject-op-1")
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	res, err := injected.Create(op, req)
	if err != nil {
		t.Fatalf("injected Authority Create: %v", err)
	}
	if res.Phase != taskauthority.PhaseQueued || res.Generation != 1 {
		t.Fatalf("injected Authority Create result = %+v", res)
	}
	agg, err := injected.Get(tid)
	if err != nil {
		t.Fatalf("injected Authority Get: %v", err)
	}
	if agg.Definition.Owner != "cli-test" || agg.Definition.Description != "task created through the injected canonical Authority" {
		t.Fatalf("injected Authority aggregate = %+v", agg.Definition)
	}
}

// TestTaskAuthorityForExactHome proves TaskAuthorityFor composes the canonical
// authority over the exact requested owning Home and never caches it on the
// context for a different Home.
func TestTaskAuthorityForExactHome(t *testing.T) {
	homeDir := t.TempDir()
	captainHome := filepath.Join(homeDir, "captains", "c1")
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := home.Init(captainHome); err != nil {
		t.Fatal(err)
	}
	ctx := Ctx{Home: homeDir}

	primary, err := ctx.TaskAuthorityFor(homeDir)
	if err != nil {
		t.Fatalf("TaskAuthorityFor(primary): %v", err)
	}
	captain, err := ctx.TaskAuthorityFor(captainHome)
	if err != nil {
		t.Fatalf("TaskAuthorityFor(captain): %v", err)
	}
	if primary.HomeID() == captain.HomeID() {
		t.Fatal("TaskAuthorityFor composed the same authority for different homes")
	}
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

// assertNoTaskAuthorityState fails when the home carries any canonical
// task-authority storage under the state root.
func assertNoTaskAuthorityState(t *testing.T, homeDir, stage string) {
	t.Helper()
	root := filepath.Join(homeDir, "state", "task-authority")
	entries, err := os.ReadDir(root)
	if err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("task-authority state created %s: %v", stage, names)
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("reading task-authority state at %s: %v", stage, err)
	}
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
