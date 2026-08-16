package fleet

import (
	"errors"
	"fmt"
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
		capID, err := r.OwnerOf(p.ID)
		if err != nil {
			return err
		}
		if capID == (domain.CaptainID{}) {
			continue
		}
		if _, exists := owned[capID.Value()]; !exists {
			return fmt.Errorf("project %s owned by unregistered captain %s", p.ID.Value(), capID.Value())
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
		// Bind, then unbind, then rebind — each retried on stale preconditions.
		ops := []struct {
			name string
			fn   func() error
		}{
			{"bind", func() error { return retryBind(t, r, cap, proj, "op-bind-"+fmt.Sprint(i)) }},
			{"unbind", func() error { return retryUnbind(t, r, cap, "op-unbind-"+fmt.Sprint(i)) }},
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

func retryBind(t *testing.T, r *Registry, capID domain.CaptainID, projID domain.ProjectID, opID string) error {
	for {
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
		if err == nil || !errors.Is(err, domain.ErrStalePrecondition) {
			return err
		}
	}
}

func retryUnbind(t *testing.T, r *Registry, capID domain.CaptainID, opID string) error {
	for {
		rev, err := r.BindingRevision()
		if err != nil {
			return err
		}
		req := UnbindCaptainRequest{
			HomeID:       r.HomeID(),
			CaptainID:    capID,
			Precondition: preconditionOf(rev),
			Reason:       "concurrent",
		}
		_, err = r.UnbindCaptain(mustOp(t, opID, req), req)
		if err == nil || !errors.Is(err, domain.ErrStalePrecondition) {
			return err
		}
	}
}

func retryRetireCaptain(t *testing.T, r *Registry, capID domain.CaptainID, opID string) error {
	for {
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
		if err == nil || !errors.Is(err, domain.ErrStalePrecondition) {
			return err
		}
	}
}

func retryRetireProject(t *testing.T, r *Registry, projID domain.ProjectID, opID string) error {
	for {
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
		if err == nil || !errors.Is(err, domain.ErrStalePrecondition) {
			return err
		}
	}
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
// sequence), then a ratio between the first and the last decile of one bind
// loop. Neither survived contact with a shared runner. The ratio form failed on
// `main` in run 31805801020 with ratio 7.7 (first-decile median 10.68ms,
// last-decile median 82.42ms, 18.02s total) while the same unchanged code
// measured ratio 1.14 and 2.05s in run 31805831782. The defect was in the
// measurement, not the threshold: the first and last deciles are taken seconds
// to tens of seconds apart, so runner load rising during the loop is
// indistinguishable from cost rising with cardinality. Recalibrating the
// threshold would only move the price of that confusion.
//
// So the two samples are taken at the same time instead. Two registries run
// side by side — one already holding n-decile bindings, one starting empty —
// and their binds are interleaved, alternating which goes first. A load spike
// now lands on both arms and cancels in the ratio; only cardinality differs
// between them. That is what the guard is for: a pathological per-binding scan
// over all stored bindings, which turns per-op cost from flat into linear in
// the number already stored.
//
// Sensitivity is unchanged from the ratio form and still set by the durable
// commit floor F: a regression adding c per stored binding yields
// (F+190c)/(F+10c), so the 3x guard trips when c > F/80. Below that floor a
// regression is out of reach of any wall-clock guard — see
// TestBindCostShapeRatioSeparatesLoadFromCardinality for what this ratio does
// and does not catch, asserted on samples rather than on a runner.
func TestRegistryBindingScaleBound(t *testing.T) {
	const n = 200
	const decile = n / 10

	// loaded is the arm under test; control is the same code at low
	// cardinality, on its own home.
	loaded, _, _ := newTestRegistry(t)
	control, _, _ := newTestRegistry(t)
	start := time.Now()
	for i := 0; i < n; i++ {
		mustRegisterProject(t, loaded, fmt.Sprintf("scale-proj-%d", i))
	}
	for i := 0; i < n; i++ {
		mustRegisterCaptain(t, loaded, fmt.Sprintf("scale-cap-%d", i))
	}
	for i := 0; i < decile; i++ {
		mustRegisterProject(t, control, fmt.Sprintf("ctl-proj-%d", i))
		mustRegisterCaptain(t, control, fmt.Sprintf("ctl-cap-%d", i))
	}

	// Grow the loaded registry to n-decile stored bindings. Unmeasured: these
	// binds only build the cardinality the measured ones run against.
	for i := 0; i < n-decile; i++ {
		mustBind(t, loaded, fmt.Sprintf("scale-cap-%d", i), fmt.Sprintf("scale-proj-%d", i))
	}

	// Interleaved measurement. Alternating which arm binds first keeps a
	// systematic per-pair effect (cache warmth, a periodic background task)
	// from landing on the same arm every time.
	high := make([]time.Duration, 0, decile)
	low := make([]time.Duration, 0, decile)
	for i := 0; i < decile; i++ {
		bindHigh := func() {
			at := time.Now()
			mustBind(t, loaded, fmt.Sprintf("scale-cap-%d", n-decile+i), fmt.Sprintf("scale-proj-%d", n-decile+i))
			high = append(high, time.Since(at))
		}
		bindLow := func() {
			at := time.Now()
			mustBind(t, control, fmt.Sprintf("ctl-cap-%d", i), fmt.Sprintf("ctl-proj-%d", i))
			low = append(low, time.Since(at))
		}
		if i%2 == 0 {
			bindHigh()
			bindLow()
		} else {
			bindLow()
			bindHigh()
		}
	}
	// Elapsed is evidence, not an assertion: logged so a genuinely slow run is
	// still visible in CI output without being a failure signal on its own.
	t.Logf("%d projects + %d captains + %d bindings (+%d control bindings) in %v", n, n, n, decile, time.Since(start))

	// Hard invariant: every binding is readable and one-to-one.
	for i := 0; i < n; i++ {
		owner, err := loaded.OwnerOf(mustProjectID(t, fmt.Sprintf("scale-proj-%d", i)))
		if err != nil {
			t.Fatalf("owner of project %d: %v", i, err)
		}
		if owner != mustCaptainID(t, fmt.Sprintf("scale-cap-%d", i)) {
			t.Fatalf("project %d owned by %s, want cap-%d", i, owner.Value(), i)
		}
	}

	ratio, highMedian, lowMedian, err := bindCostShapeRatio(high, low)
	t.Logf("per-binding median: %v against %d stored bindings, %v against a fresh registry", highMedian, n-decile, lowMedian)
	if err != nil {
		t.Fatalf("cost shape not judgeable: %v", err)
	}
	if ratio > 3 {
		t.Fatalf("per-binding cost is %.1fx higher against %d stored bindings than against a fresh registry "+
			"measured in the same window (median %v vs %v): binding cost scales with the number of stored "+
			"bindings, which is the pathological per-binding scan this guard exists to catch",
			ratio, n-decile, highMedian, lowMedian)
	}
}

// bindCostShapeRatio reports how much more a bind costs against many stored
// bindings than against almost none, from two interleaved sample sets. Medians,
// not means, so a single scheduling stall on a loaded runner cannot move the
// verdict — and because the samples are interleaved, load that lasts longer
// than one bind moves both medians together and cancels.
func bindCostShapeRatio(high, low []time.Duration) (ratio float64, highMedian, lowMedian time.Duration, err error) {
	if len(high) == 0 || len(low) == 0 {
		return 0, 0, 0, fmt.Errorf("need samples from both arms, got %d high and %d low", len(high), len(low))
	}
	highMedian = medianDuration(high)
	lowMedian = medianDuration(low)
	if lowMedian <= 0 {
		return 0, highMedian, lowMedian, fmt.Errorf("low-cardinality median is %v — clock resolution too coarse to judge cost shape", lowMedian)
	}
	return float64(highMedian) / float64(lowMedian), highMedian, lowMedian, nil
}

// TestBindCostShapeRatioSeparatesLoadFromCardinality asserts the two properties
// the guard above is claimed to have, on samples instead of on a runner: load
// that moves both arms must not trip it, and a per-stored-binding cost must.
// The first case is the failure this measurement was rebuilt to remove — it
// carries the shape of run 31805801020, where a load ramp during one bind loop
// produced ratio 7.7 with no code change.
func TestBindCostShapeRatioSeparatesLoadFromCardinality(t *testing.T) {
	const samples = 20
	const floor = 5 * time.Millisecond

	// Case 1: flat per-binding cost, runner load ramping 8x across the window
	// and spiking hard in the middle. Interleaved samples see it together.
	var rampHigh, rampLow []time.Duration
	for i := 0; i < samples; i++ {
		load := time.Duration(1+7*i/samples) * floor
		if i == samples/2 {
			load = 40 * floor
		}
		rampHigh = append(rampHigh, floor+load)
		rampLow = append(rampLow, floor+load)
	}
	ratio, high, low, err := bindCostShapeRatio(rampHigh, rampLow)
	if err != nil {
		t.Fatalf("ramp case: %v", err)
	}
	if ratio > 3 {
		t.Fatalf("runner load alone tripped the guard: ratio %.1f (median %v vs %v)", ratio, high, low)
	}

	// Case 2: a per-binding scan over stored bindings, c per stored binding,
	// measured at 180 stored vs 0 stored — with the same load ramp on top. The
	// ramp raises the effective floor, so it also raises the c a 3x ratio can
	// see: that is the cost of using wall-clock at all, stated rather than
	// hidden.
	const c = 500 * time.Microsecond
	var scanHigh, scanLow []time.Duration
	for i := 0; i < samples; i++ {
		load := time.Duration(1+7*i/samples) * floor
		scanHigh = append(scanHigh, floor+load+180*c)
		scanLow = append(scanLow, floor+load)
	}
	ratio, high, low, err = bindCostShapeRatio(scanHigh, scanLow)
	if err != nil {
		t.Fatalf("scan case: %v", err)
	}
	if ratio <= 3 {
		t.Fatalf("a per-stored-binding cost of %v did not trip the guard: ratio %.1f (median %v vs %v)", c, ratio, high, low)
	}
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
