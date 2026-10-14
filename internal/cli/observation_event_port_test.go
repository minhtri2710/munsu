// Tests for the CLI-side native observation event port: endpoint
// scanning, backend resolution, and source wiring. Wire behavior is exercised
// with a fake herdr executable; endpoints without an event surface must be
// omitted so the orchestrator keeps pure polling.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestObservationEventPort_Sources_HerdrEndpoint(t *testing.T) {
	homeDir := t.TempDir()
	mustBindCLIEndpoint(t, homeDir, "task-1", taskauthority.EndpointBinding{Backend: "herdr", Handle: "w:p"})

	// Fake herdr on PATH with a ready protocol so resolution succeeds.
	fakeDir := t.TempDir()
	writeFakeHerdrCLI(t, fakeDir)
	testutil.PrependPath(t, fakeDir)

	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("Sources = %d, want 1", len(sources))
	}
	if sources[0].Endpoint.Backend != "herdr" || sources[0].Endpoint.Handle != "w:p" {
		t.Errorf("endpoint = %+v, want herdr w:p", sources[0].Endpoint)
	}
	if sources[0].Source == nil {
		t.Error("source must be non-nil for a herdr endpoint")
	}
	if _, ok := sources[0].Source.(backend.ObservationEventSource); !ok {
		t.Errorf("source type %T does not implement ObservationEventSource", sources[0].Source)
	}
}

func TestObservationEventPort_Sources_BackendWithoutEventsOmitted(t *testing.T) {
	homeDir := t.TempDir()
	// tmux has no native event surface; the port must omit it entirely.
	mustBindCLIEndpoint(t, homeDir, "task-tmux", taskauthority.EndpointBinding{Backend: "tmux", Handle: "sess:1"})
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 (tmux has no event surface)", len(sources))
	}
}

func TestObservationEventPort_Sources_NoBinding(t *testing.T) {
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(t.TempDir())
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 for empty home", len(sources))
	}
}

// A .meta window (a captain's, or a stale soldier projection) is not a bound
// endpoint: only the canonical binding is an event-wait target.
func TestObservationEventPort_Sources_MetaWindowSkipped(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	mustWriteCLIMeta(t, homeDir, "cap-1", map[string]string{"backend": "herdr", "window": "w:cap", "kind": "captain"})
	mustWriteCLIMeta(t, homeDir, "task-1", map[string]string{"backend": "herdr", "window": "w:p"})
	fakeDir := t.TempDir()
	writeFakeHerdrCLI(t, fakeDir)
	testutil.PrependPath(t, fakeDir)
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 (no canonical binding)", len(sources))
	}
}

func TestObservationEventPort_Sources_UnresolvableSkipped(t *testing.T) {
	homeDir := t.TempDir()
	mustBindCLIEndpoint(t, homeDir, "task-1", taskauthority.EndpointBinding{Backend: "herdr", Handle: "w:p"})
	// No herdr on PATH → BackendForTask fails → endpoint omitted → pure polling.
	t.Setenv("PATH", "/nonexistent")
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 (unresolvable backend → poll)", len(sources))
	}
}

// mustBindCLIEndpoint commits a live, bound ship task through the canonical
// operations: create, bind its worktree, bind ep as its endpoint.
func mustBindCLIEndpoint(t *testing.T, homeDir, id string, ep taskauthority.EndpointBinding) {
	t.Helper()
	h, err := home.Open(homeDir)
	if errors.Is(err, home.ErrNotInitialized) {
		h, err = home.Init(homeDir)
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	op := func(name string, intent domain.Intent) domain.Operation {
		t.Helper()
		opID, err := domain.NewOperationID("op-" + name + "-" + id)
		if err != nil {
			t.Fatal(err)
		}
		o, err := domain.NewOperation(opID, intent)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	precondition := func() domain.Precondition {
		agg, err := c.Get(tid)
		if err != nil {
			t.Fatal(err)
		}
		return domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	}
	create := taskauthority.CanonicalCreateRequest{HomeID: c.HomeID(), TaskID: tid, Owner: "owner", Description: "work", Kind: taskauthority.KindShip, Reason: "create"}
	if _, err := c.Create(op("create", create), create); err != nil {
		t.Fatal(err)
	}
	wt := taskauthority.CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: tid, Precondition: precondition(), Reason: "bind", Binding: taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo", Path: "/wt/" + id, GitDir: "/repo/.git/worktrees/" + id, CommonDir: "/repo/.git",
		BaseHead: strings.Repeat("a", 40), LeaseID: "lease-wt", FenceToken: "fence-wt", BoundAtUnix: 1,
	}}
	if _, err := c.BindWorktree(op("bind-wt", wt), wt); err != nil {
		t.Fatal(err)
	}
	ep.LeaseID, ep.FenceToken, ep.Incarnation, ep.BoundAtUnix = "lease-ep", "fence-ep", "inc-ep", 1
	bind := taskauthority.CanonicalBindEndpointRequest{HomeID: c.HomeID(), TaskID: tid, Precondition: precondition(), Binding: ep, Reason: "bind"}
	if _, err := c.BindEndpoint(op("bind-ep", bind), bind); err != nil {
		t.Fatal(err)
	}
}

func mustWriteCLIMeta(t *testing.T, homeDir, id string, kv map[string]string) {
	t.Helper()
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for k, v := range kv {
		b = append(b, []byte(k+"="+v+"\n")...)
	}
	metaPath, err := home.MetaFilePath(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, b, 0644); err != nil {
		t.Fatal(err)
	}
}

// writeFakeHerdrCLI creates a minimal herdr binary responding to the schema
// probe (protocol 17) so backend resolution succeeds.
func writeFakeHerdrCLI(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "herdr 0.7.5"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  echo '{"protocol":17,"schema_version":1,"schemas":{}}'` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
}
