package fleet

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- Scalability gate: independent aggregates do not serialize -------------

// TestRegistryIndependentProjectAndCaptainOpsOverlap proves that independent
// Project and Captain registration operations OVERLAP rather than serialize on
// a registry-wide lock. The test holds the Project aggregate lock (a real
// home fenced lock on the causal path) and verifies a concurrent Captain
// registration completes without waiting on it. If the two aggregates shared a
// lock, the Captain operation would block for the full acquisition budget and
// fail with ErrLockTimeout; completing well inside that budget proves overlap.
func TestRegistryIndependentProjectAndCaptainOpsOverlap(t *testing.T) {
	r, h, _ := newTestRegistry(t)

	// Hold the Project aggregate lock, simulating an in-flight Project op.
	plk, err := h.Lock(projectRegistryScope)
	if err != nil {
		t.Fatal(err)
	}
	defer plk.Release()

	captainID := mustCaptainID(t, "c-overlap")
	rev, err := r.CaptainRevision()
	if err != nil {
		t.Fatal(err)
	}
	req := RegisterCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    captainID,
		Home:         "/captain/overlap",
		Scope:        "domain",
		Precondition: preconditionOf(rev),
		Reason:       "overlap",
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.RegisterCaptain(mustOp(t, "op-captain-overlap", req), req)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("captain registration while project lock held: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("captain registration blocked on the project aggregate lock — aggregates serialize")
	}

	// The captain must be durably registered (its operation ran to completion
	// on the real home while the project lock was held).
	if _, err := r.GetCaptain(captainID); err != nil {
		t.Fatalf("captain not registered after overlap: %v", err)
	}
}

// TestRegistryIndependentCaptainAndProjectOpsOverlap is the symmetric case:
// holding the Captain aggregate lock must not block Project registration.
func TestRegistryIndependentCaptainAndProjectOpsOverlap(t *testing.T) {
	r, h, _ := newTestRegistry(t)

	clk, err := h.Lock(captainRegistryScope)
	if err != nil {
		t.Fatal(err)
	}
	defer clk.Release()

	projectID := mustProjectID(t, "p-overlap")
	rev, err := r.ProjectRevision()
	if err != nil {
		t.Fatal(err)
	}
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    projectID,
		Name:         "p-overlap",
		Path:         "/proj/overlap",
		Precondition: preconditionOf(rev),
		Reason:       "overlap",
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.RegisterProject(mustOp(t, "op-project-overlap", req), req)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("project registration while captain lock held: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project registration blocked on the captain aggregate lock — aggregates serialize")
	}

	if _, err := r.GetProject(projectID); err != nil {
		t.Fatalf("project not registered after overlap: %v", err)
	}
}

// --- Competing binds/retires: deadlock-free, no contradictory ownership -----

// registryMutator is one concurrent lifecycle worker that retries a mutation
// on stale preconditions (truthful conflict/retry behavior).
type registryMutator struct {
	id  string
	run func(*Registry, string) error
}

func runRegistryWorkers(t *testing.T, workers int, fn func(*Registry, int) error) error {
	t.Helper()
	r, _, _ := newTestRegistry(t)
	// Seed distinct projects and captains for the workers up front.
	for i := 0; i < workers; i++ {
		mustRegisterProject(t, r, fmt.Sprintf("proj-%d", i))
		mustRegisterCaptain(t, r, fmt.Sprintf("cap-%d", i))
	}
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- fn(r, i)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	// Invariants: at most one owning Captain per Project, at most one Project
	// per Captain, and every binding references registered entities.
	projects, err := r.ListProjects()
	if err != nil {
		return err
	}
	captains, err := r.ListCaptains()
	if err != nil {
		return err
	}
	owner := map[string]string{}
	owned := map[string]string{}
	for _, c := range captains {
		p, err := r.ProjectOf(c.ID)
		if err != nil {
			return err
		}
		if p == (domain.ProjectID{}) {
			continue
		}
		if prev, exists := owner[p.Value()]; exists {
			return fmt.Errorf("project %s owned by both %s and %s", p.Value(), prev, c.ID.Value())
		}
		owner[p.Value()] = c.ID.Value()
		owned[c.ID.Value()] = p.Value()
	}
	for _, p := range projects {
		capID, err := bindingOwnerOf(r, p.ID.Value())
		if err != nil {
			return err
		}
		if capID == "" {
			continue
		}
		if _, exists := owned[capID]; !exists {
			return fmt.Errorf("project %s owned by unregistered captain %s", p.ID.Value(), capID)
		}
	}
	return nil
}

// TestRegistryCompetingBindsRetiresNoDeadlockNoContradictoryOwnership drives
// concurrent Bind/Unbind/Retire of disjoint entity pairs under -race. Every
// mutation retries on stale preconditions; the invariant check after the
// workers join proves no deadlock (all complete) and no contradictory
// ownership.
func TestRegistryCompetingBindsRetiresNoDeadlockNoContradictoryOwnership(t *testing.T) {
	const workers = 6
	err := runRegistryWorkers(t, workers, func(r *Registry, i int) error {
		proj := mustProjectID(t, fmt.Sprintf("proj-%d", i))
		cap := mustCaptainID(t, fmt.Sprintf("cap-%d", i))
		// Bind, then rebind, then retire — each re-driven for as long as it
		// fails from contention (see retryContended).
		ops := []struct {
			name string
			fn   func() error
		}{
			{"bind", func() error { return retryBind(t, r, cap, proj, "op-bind-"+fmt.Sprint(i)) }},
			{"bind-again", func() error { return retryBind(t, r, cap, proj, "op-rebind-"+fmt.Sprint(i)) }},
			{"retire-cap", func() error { return retryRetireCaptain(t, r, cap, "op-retire-cap-"+fmt.Sprint(i)) }},
			{"retire-proj", func() error { return retryRetireProject(t, r, proj, "op-retire-proj-"+fmt.Sprint(i)) }},
		}
		for _, op := range ops {
			if err := op.fn(); err != nil {
				return fmt.Errorf("worker %d %s: %w", i, op.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("competing binds/retires: %v", err)
	}
}

// retryLoopBudget bounds every retry loop below. It is a liveness bound on the
// worker, not a second lock budget: a genuine deadlock never releases the
// scope, so every re-drive keeps failing, the worker runs the budget out and
// names the deadlock here instead of hanging until the package timeout. That
// is the whole claim, and it does not depend on how fast any one machine is —
// the constant only has to sit far enough above this workload's own runtime
// that ordinary contention never reaches it.
const retryLoopBudget = 90 * time.Second

// retryContended re-drives a mutation for as long as it fails from contention
// rather than from a broken invariant.
//
// domain.ErrStalePrecondition: another worker committed first — the original
// retry condition.
//
// home.ErrLockTimeout: the binding scope was held by other workers for the
// whole acquisition budget. Every mutation here serializes on that one scope,
// so a loser can be starved out of a single acquisition without anything being
// wrong. This is Home.Lock behaving as designed, so the worker re-drives. The
// budget itself stays pinned by TestLockHeldScopeTimesOut in internal/home,
// which is where a regression in it belongs.
//
// Neither retry costs this test its deadlock claim: both are bounded by
// retryLoopBudget, and a deadlocked scope exhausts it.
func retryContended(t *testing.T, what string, attempt func() error) error {
	t.Helper()
	deadline := time.Now().Add(retryLoopBudget)
	for {
		err := attempt()
		if err == nil {
			return nil
		}
		if !errors.Is(err, domain.ErrStalePrecondition) && !errors.Is(err, home.ErrLockTimeout) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s never completed within %s, so the scope is deadlocked or livelocked: %w", what, retryLoopBudget, err)
		}
	}
}

func retryBind(t *testing.T, r *Registry, capID domain.CaptainID, projID domain.ProjectID, opID string) error {
	return retryContended(t, "bind", func() error {
		rev, err := r.BindingRevision()
		if err != nil {
			return err
		}
		req := BindCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    capID,
			ProjectID:    projID,
			Precondition: preconditionOf(rev),
			Reason:       "concurrent",
		}
		_, err = r.BindCaptain(mustOp(t, opID, req), req)
		return err
	})
}

func retryRetireCaptain(t *testing.T, r *Registry, capID domain.CaptainID, opID string) error {
	return retryContended(t, "retire captain", func() error {
		rev, err := r.CaptainRevision()
		if err != nil {
			return err
		}
		req := RetireCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    capID,
			Precondition: preconditionOf(rev),
			Reason:       "concurrent",
		}
		_, err = r.RetireCaptain(mustOp(t, opID, req), req)
		return err
	})
}

func retryRetireProject(t *testing.T, r *Registry, projID domain.ProjectID, opID string) error {
	return retryContended(t, "retire project", func() error {
		rev, err := r.ProjectRevision()
		if err != nil {
			return err
		}
		req := RetireProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    projID,
			Precondition: preconditionOf(rev),
			Reason:       "concurrent",
		}
		_, err = r.RetireProject(mustOp(t, opID, req), req)
		return err
	})
}

// TestRegistryBindLockScopeIsSmallestTruthful proves that Bind acquires only
// the binding aggregate lock, not the project or captain aggregate locks.
// While the project and captain locks are held by the test, a Bind must still
// complete: it only needs the binding scope, so unrelated registration is not
// serialized by a bind.
func TestRegistryBindLockScopeIsSmallestTruthful(t *testing.T) {
	r, h, _ := newTestRegistry(t)
	mustRegisterProject(t, r, "alpha")
	mustRegisterCaptain(t, r, "c1")

	plk, err := h.Lock(projectRegistryScope)
	if err != nil {
		t.Fatal(err)
	}
	defer plk.Release()
	clk, err := h.Lock(captainRegistryScope)
	if err != nil {
		t.Fatal(err)
	}
	defer clk.Release()

	// With BOTH the project and captain aggregate locks held by this test, a
	// Bind must still succeed: Bind serializes only on the binding aggregate.
	rev, err := r.BindingRevision()
	if err != nil {
		t.Fatal(err)
	}
	req := BindCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainID(t, "c1"),
		ProjectID:    mustProjectID(t, "alpha"),
		Precondition: preconditionOf(rev),
		Reason:       "scope",
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.BindCaptain(mustOp(t, "op-bind-scope", req), req)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("bind while project+captain locks held: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bind blocked on the project/captain aggregate locks — bind scope is broader than the binding aggregate")
	}
}

// --- Bounded scale evidence for the Binding aggregate -----------------------

// TestRegistryBindingScaleBound proves the Binding aggregate handles a stated
// representative cardinality, tracing the real encoding/read/commit path. The
// hard bound is the invariant (all N bindings readable, one owner each); the
// cost bound is the SHAPE of per-binding cost, not its absolute value.
//
// The guard used to be an absolute wall-clock ceiling (60s for the whole
// sequence), then a ratio between wall-clock deciles of the same run. Both were
// wall-clock, and wall-clock cannot carry this signal on a shared runner. The
// ratio form assumed a ~110ms durable-commit floor F, which is where its
// sensitivity came from; that floor was measured on the -race lane and does not
// exist on the integration lane, where the same code measured a 3.4ms floor and
// a 2.05s total on one run against a 10.7ms floor and an 18.02s total on
// another (BEO-82). A ratio cancels a constant speed factor, not a load change
// that happens *during* the measurement — and the first and last deciles are
// seconds apart, so runner load drifting between them reads exactly like
// per-binding cost growth. That is what failed run 31805801020 on unchanged
// code.
//
// What the guard actually needs to catch is a pathological regression: an
// accidental per-binding scan over all existing bindings, which turns per-op
// work from flat into linear in the number of bindings already stored. That is
// a count, not a duration. A healthy bind reads a fixed set of documents
// (receipt, project registry, captain registry, binding doc) no matter how many
// bindings are stored — the binding doc gets bigger, but it is still read once.
// A per-binding re-read or per-binding fsync multiplies that count by the
// stored cardinality. So the guard asserts on read syscalls per bind, which no
// amount of runner load can inflate, and keeps wall-clock only as logged
// evidence.
func TestRegistryBindingScaleBound(t *testing.T) {
	const n = 200
	r, _, _ := newTestRegistry(t)
	start := time.Now()
	for i := 0; i < n; i++ {
		mustRegisterProject(t, r, fmt.Sprintf("scale-proj-%d", i))
	}
	for i := 0; i < n; i++ {
		mustRegisterCaptain(t, r, fmt.Sprintf("scale-cap-%d", i))
	}
	perBind := make([]time.Duration, n)
	perBindReads := make([]int64, n)
	_, countable := readSyscalls()
	for i := 0; i < n; i++ {
		readsBefore, _ := readSyscalls()
		bindStart := time.Now()
		mustBind(t, r, fmt.Sprintf("scale-cap-%d", i), fmt.Sprintf("scale-proj-%d", i))
		perBind[i] = time.Since(bindStart)
		readsAfter, _ := readSyscalls()
		perBindReads[i] = readsAfter - readsBefore
	}
	// Elapsed is evidence, not an assertion: logged so a genuinely slow run is
	// still visible in CI output without being a failure signal on its own.
	t.Logf("%d projects + %d captains + %d bindings in %v", n, n, n, time.Since(start))

	// Hard invariant: every binding is readable and one-to-one.
	for i := 0; i < n; i++ {
		owner, err := bindingOwnerOf(r, fmt.Sprintf("scale-proj-%d", i))
		if err != nil {
			t.Fatalf("owner of project %d: %v", i, err)
		}
		if owner != fmt.Sprintf("scale-cap-%d", i) {
			t.Fatalf("project %d owned by %s, want cap-%d", i, owner, i)
		}
	}

	// Evidence, not assertions: durations are logged so a genuinely slow run
	// stays visible, and they are what the shape guard used to be built on.
	const decile = n / 10
	t.Logf("per-binding median duration: first decile %v, last decile %v",
		medianDuration(perBind[:decile]), medianDuration(perBind[n-decile:]))

	// Shape guard: the number of documents a bind reads must stay flat as the
	// stored binding count grows 10x. Medians, not means, so a stray read from
	// elsewhere in the process cannot move the verdict.
	if !countable {
		t.Logf("per-binding read-syscall counts are unavailable on %s, so the cost-shape guard is not asserted here; "+
			"the CI lane that this guard exists for runs on Linux", runtime.GOOS)
		return
	}
	firstReads := medianInt64(perBindReads[:decile])
	lastReads := medianInt64(perBindReads[n-decile:])
	t.Logf("per-binding read syscalls: first decile %d, last decile %d", firstReads, lastReads)
	if firstReads <= 0 {
		t.Fatalf("first-decile median is %d read syscalls — the counter is not observing the commit path", firstReads)
	}
	if ratio := float64(lastReads) / float64(firstReads); ratio > 3 {
		t.Fatalf("per-binding document reads grew %.1fx from the first to the last decile "+
			"(median %d -> %d): binding cost scales with the number of stored "+
			"bindings, which is the pathological per-binding scan this guard exists to catch",
			ratio, firstReads, lastReads)
	}
}

// medianInt64 returns the median of samples without mutating the caller's
// slice. Callers pass windows of a live measurement series.
func medianInt64(samples []int64) int64 {
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	return sorted[len(sorted)/2]
}

// medianDuration returns the median of samples without mutating the caller's
// slice. Callers pass windows of a live measurement series.
func medianDuration(samples []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	return sorted[len(sorted)/2]
}

// BenchmarkRegistryBindingAggregate measures per-binding cost (elapsed and
// allocations) over the real home-backed commit path. The benchmark is the
// operation-count bound: each iteration is one bind on its own revision.
func BenchmarkRegistryBindingAggregate(b *testing.B) {
	r, _, _ := newTestRegistryB(b)
	// Pre-register projects and captains up front; the measured loop is the
	// binding aggregate only.
	for i := 0; i < b.N; i++ {
		mustRegisterProjectB(b, r, fmt.Sprintf("bproj-%d", i))
		mustRegisterCaptainB(b, r, fmt.Sprintf("bcap-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rev, err := r.BindingRevision()
		if err != nil {
			b.Fatal(err)
		}
		req := BindCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    mustCaptainIDB(b, fmt.Sprintf("bcap-%d", i)),
			ProjectID:    mustProjectIDB(b, fmt.Sprintf("bproj-%d", i)),
			Precondition: preconditionOf(rev),
			Reason:       "bench",
		}
		if _, err := r.BindCaptain(mustOpB(b, fmt.Sprintf("op-bench-bind-%d", i), req), req); err != nil {
			b.Fatal(err)
		}
	}
}

type benchTB interface {
	Helper()
	TempDir() string
	Fatalf(string, ...any)
}

func newTestRegistryB(b benchTB) (*Registry, *home.Home, string) {
	root := b.TempDir()
	h, err := home.Init(root)
	if err != nil {
		b.Fatalf("home.Init: %v", err)
	}
	r, err := NewRegistry(h)
	if err != nil {
		b.Fatalf("NewRegistry: %v", err)
	}
	return r, h, root
}

func mustOpB(b benchTB, id string, intent domain.Intent) domain.Operation {
	opID, err := domain.NewOperationID(id)
	if err != nil {
		b.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		b.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}

func mustProjectIDB(b benchTB, value string) domain.ProjectID {
	id, err := domain.NewProjectID(value)
	if err != nil {
		b.Fatalf("NewProjectID(%s): %v", value, err)
	}
	return id
}

func mustCaptainIDB(b benchTB, value string) domain.CaptainID {
	id, err := domain.NewCaptainID(value)
	if err != nil {
		b.Fatalf("NewCaptainID(%s): %v", value, err)
	}
	return id
}

func mustRegisterProjectB(b benchTB, r *Registry, name string) {
	rev, err := r.ProjectRevision()
	if err != nil {
		b.Fatalf("ProjectRevision: %v", err)
	}
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    mustProjectIDB(b, name),
		Name:         name,
		Path:         "/proj/" + name,
		Mode:         "no-mistakes",
		Precondition: preconditionOf(rev),
		Reason:       "register",
	}
	if _, err := r.RegisterProject(mustOpB(b, "op-reg-proj-b-"+name, req), req); err != nil {
		b.Fatalf("RegisterProject(%s): %v", name, err)
	}
}

func mustRegisterCaptainB(b benchTB, r *Registry, id string) {
	rev, err := r.CaptainRevision()
	if err != nil {
		b.Fatalf("CaptainRevision: %v", err)
	}
	req := RegisterCaptainRequest{
		HomeID:       r.HomeID(),
		CaptainID:    mustCaptainIDB(b, id),
		Home:         "/captain/" + id,
		Scope:        "domain",
		Precondition: preconditionOf(rev),
		Reason:       "register",
	}
	if _, err := r.RegisterCaptain(mustOpB(b, "op-reg-cap-b-"+id, req), req); err != nil {
		b.Fatalf("RegisterCaptain(%s): %v", id, err)
	}
}
