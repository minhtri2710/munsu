//go:build integration

package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// deliveryTestHead is the full 40-hex Git object ID carried by the delivery
// identity and the bound worktree head in the delivery tests.
const deliveryTestHead = "abc123def456abc123def456abc123def456abc1"

// deliveryTestBase is the base ref the delivery identity pins and the scripted
// provider observations report, so the tests exercise the pre-mutation base
// ref fence in its accepting direction.
const deliveryTestBase = "main"

// fakeDeliveryProvider is a recording fake DeliveryProvider for the
// journaled delivery state-machine tests. It consumes scripted observations
// in order and records every irreversible Merge call.
type fakeDeliveryProvider struct {
	merges       int
	mergeErr     error
	validateErr  error
	requests     []DeliveryMergeRequest
	observations []DeliveryProviderObservation
	observeErrs  []error
	onMerge      func()
}

func newFakeDeliveryProvider() *fakeDeliveryProvider {
	return &fakeDeliveryProvider{}
}

func (f *fakeDeliveryProvider) script(obs ...DeliveryProviderObservation) *fakeDeliveryProvider {
	f.observations = append(f.observations, obs...)
	return f
}

func (f *fakeDeliveryProvider) scriptErr(errs ...error) *fakeDeliveryProvider {
	f.observeErrs = append(f.observeErrs, errs...)
	return f
}

func (f *fakeDeliveryProvider) ValidateMergeRequest(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	f.requests = append(f.requests, request)
	return f.validateErr
}

func (f *fakeDeliveryProvider) Merge(ident domain.DeliveryIdentity, request DeliveryMergeRequest) error {
	f.merges++
	if f.onMerge != nil {
		f.onMerge()
	}
	return f.mergeErr
}

func (f *fakeDeliveryProvider) Observe(ident domain.DeliveryIdentity) (DeliveryProviderObservation, error) {
	if len(f.observeErrs) > 0 {
		err := f.observeErrs[0]
		f.observeErrs = f.observeErrs[1:]
		return DeliveryProviderObservation{}, err
	}
	if len(f.observations) > 0 {
		obs := f.observations[0]
		f.observations = f.observations[1:]
		return obs, nil
	}
	return DeliveryProviderObservation{State: "OPEN", HeadSHA: ident.HeadSHA, BaseRef: ident.BaseRef, Mergeability: DeliveryMergeabilityAllowed}, nil
}

// deliveryFixtureIdentity is the typed delivery identity the deliver tests
// execute under; its head equals the bound worktree head.
func deliveryTestIdentity() domain.DeliveryIdentity {
	return domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    deliveryTestBase,
		HeadRef:    "feature/delivery",
		HeadSHA:    deliveryTestHead,
		CapturedAt: "2026-08-05T00:00:00Z",
	}
}

// deliverRequest is the standard journaled delivery intent for the tests.
func deliverRequest() DeliverRequest {
	return DeliverRequest{
		Kind:     taskauthority.DeliveryAuthorizationProviderMerge,
		Identity: deliveryTestIdentity(),
		Method:   "squash",
		Preconditions: []taskauthority.DeliveryPrecondition{
			taskauthority.DeliveryPreconditionPRMergeable,
			taskauthority.DeliveryPreconditionPRHeadCurrent,
		},
	}
}

// mustWorkingDeliveryTask creates a task and binds the worktree (at the
// delivery identity head) and endpoint so it is working (revision 3) with
// the exact delivery bindings.
func mustWorkingDeliveryTask(t *testing.T, c *taskauthority.Canonical, taskID string) {
	t.Helper()
	mustFleetCreate(t, c, taskID)
	tid := mustFleetTaskID(t, taskID)
	agg, err := c.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	bw := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-" + taskID,
			Path:               filepath.Join("/worktrees", taskID),
			GitDir:             filepath.Join("/worktrees", taskID, ".git"),
			CommonDir:          "/repo/.git",
			Head:               deliveryTestHead,
			LeaseID:            "lease-wt-" + taskID,
			FenceToken:         "fence-wt-" + taskID,
			BoundAtUnix:        time.Now().Unix(),
		},
		Reason: "spawn",
	}
	if _, err := c.BindWorktree(mustFleetOperation(t, "op-del-bindwt-"+taskID, bw), bw); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
	agg2, err := c.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	be := taskauthority.CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg2.Generation), uint64(agg2.Revision)),
		Binding: taskauthority.EndpointBinding{
			Backend:      "tmux",
			Handle:       "@1",
			LeaseID:      "lease-ep-" + taskID,
			FenceToken:   "fence-ep-" + taskID,
			SessionOwner: "session-" + taskID,
			Incarnation:  "inc-" + taskID, // opaque launch incarnation (BEO-16/P1a contract)
			BoundAtUnix:  time.Now().Unix(),
		},
		Reason: "spawn",
	}
	if _, err := c.BindEndpoint(mustFleetOperation(t, "op-del-bindep-"+taskID, be), be); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", taskID, err)
	}
}

// installDeliveryProviderFor swaps the production capability resolver with a
// recording fake and restores it on cleanup.
func installDeliveryProviderFor(t *testing.T, provider *fakeDeliveryProvider) {
	t.Helper()
	old := deliveryProviderFor
	deliveryProviderFor = func(domain.DeliveryIdentity) (DeliveryProvider, error) {
		return provider, nil
	}
	t.Cleanup(func() { deliveryProviderFor = old })
}

// installScriptedProviderFor installs a fake provider driven by a named
// script: "open-then-merged", "open-then-unreachable", "open", "merged",
// "unreachable".
func installScriptedProviderFor(t *testing.T, script string) *fakeDeliveryProvider {
	t.Helper()
	provider := newFakeDeliveryProvider()
	switch script {
	case "open-then-merged":
		provider.script(
			DeliveryProviderObservation{State: "OPEN", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, Mergeability: DeliveryMergeabilityAllowed},
			DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		)
	case "open-then-unreachable":
		provider.script(
			DeliveryProviderObservation{State: "OPEN", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase},
		)
		provider.scriptErr(fmt.Errorf("provider unreachable after merge"))
	case "open":
		provider.script(DeliveryProviderObservation{State: "OPEN", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, Mergeability: DeliveryMergeabilityAllowed})
	case "merged":
		provider.script(DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase, MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
	case "closed":
		provider.script(DeliveryProviderObservation{State: "CLOSED", HeadSHA: deliveryTestHead, BaseRef: deliveryTestBase})
	case "unreachable":
		provider.scriptErr(fmt.Errorf("provider unreachable"))
	default:
		t.Fatalf("unknown provider script %q", script)
	}
	installDeliveryProviderFor(t, provider)
	return provider
}

// readDeliveryJournalRecord reads the terminal/active journal record of one
// delivery journal ID from the home state root.
func readDeliveryJournalRecord(t *testing.T, homeDir, journalID string) (*deliveryJournal, error) {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := h.Read(home.RootState, deliveryJournalKey(journalID))
	if err != nil {
		return nil, err
	}
	var journal deliveryJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, err
	}
	return &journal, nil
}

// listActiveDeliveryJournals reads the bounded active index of the home.
func listActiveDeliveryJournals(t *testing.T, homeDir string) []string {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := readDeliveryIndex(h)
	if err != nil {
		t.Fatal(err)
	}
	return idx.Active
}

// listDeliveryJournalFiles lists the retained journal record files (active
// and completed) of the home, excluding the bounded index document.
func listDeliveryJournalFiles(t *testing.T, homeDir string) []string {
	t.Helper()
	dir := filepath.Join(homeDir, "state", deliveryJournalDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() == "index.json" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	return out
}

// runDeliveryCrashHelper runs the current test binary in a subprocess that
// performs one Deliver with the delivery crash hook armed to exit at the
// given boundary, using the given provider script. It asserts the subprocess
// exited with the crash code.
func runDeliveryCrashHelper(t *testing.T, homeDir, taskID, boundary, script string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestDeliverCrashHelper$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_DELIVERY_CRASH_HELPER=1",
		"MUNSU_DELIVERY_CRASH_AFTER="+boundary,
		"MUNSU_DELIVERY_HOME="+homeDir,
		"MUNSU_DELIVERY_TASK="+taskID,
		"MUNSU_DELIVERY_SCRIPT="+script,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected helper subprocess to crash at %s", boundary)
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exited at boundary %s: %v\n%s", boundary, err, output)
	}
}

// TestDeliverCrashHelper is the subprocess entry point for the delivery
// crash tests: it arms the crash hook and executes one Deliver.
func TestDeliverCrashHelper(t *testing.T) {
	if os.Getenv("MUNSU_DELIVERY_CRASH_HELPER") != "1" {
		return
	}
	boundary := os.Getenv("MUNSU_DELIVERY_CRASH_AFTER")
	deliveryCrashHook = func(got string) {
		if got == boundary {
			os.Exit(92)
		}
	}
	provider := installScriptedProviderFor(t, os.Getenv("MUNSU_DELIVERY_SCRIPT"))
	_ = provider
	if _, err := Deliver(os.Getenv("MUNSU_DELIVERY_HOME"), os.Getenv("MUNSU_DELIVERY_TASK"), deliverRequest()); err != nil {
		// The crash boundary exits before returning; reaching here without a
		// crash is also fine (the boundary was never hit).
		fmt.Fprintf(os.Stderr, "deliver: %v\n", err)
	}
	os.Exit(0)
}
