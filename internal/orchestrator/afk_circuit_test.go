package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- CircuitKey tests ---

func TestCircuitKeyIdentity(t *testing.T) {
	// Same target, input, signature → same key.
	k1 := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}
	k2 := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}
	if k1 != k2 {
		t.Fatal("identical CircuitKey values differ")
	}
}

func TestCircuitKeyChangedInputNewSeries(t *testing.T) {
	// Different input → different key (new series).
	k1 := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}
	k2 := CircuitKey{Target: "s:p", Input: "task-2", Signature: "sig-a"}
	if k1 == k2 {
		t.Fatal("different inputs produce same key, want different")
	}
}

func TestCircuitKeyChangedSignatureNewSeries(t *testing.T) {
	// Different signature → different key.
	k1 := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}
	k2 := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-b"}
	if k1 == k2 {
		t.Fatal("different signatures produce same key, want different")
	}
}

func TestCircuitKeyChangedTargetNewSeries(t *testing.T) {
	// Different target → different key.
	k1 := CircuitKey{Target: "s:p1", Input: "task-1", Signature: "sig-a"}
	k2 := CircuitKey{Target: "s:p2", Input: "task-1", Signature: "sig-a"}
	if k1 == k2 {
		t.Fatal("different targets produce same key, want different")
	}
}

// --- Circuit tests ---

func TestCircuitClosedInitially(t *testing.T) {
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 3,
			Window:      5 * time.Minute,
		},
	}
	if c.State != CircuitClosed {
		t.Errorf("initial state = %v, want closed", c.State)
	}
}

func TestCircuitRecordOpensOnExceededBudget(t *testing.T) {
	now := time.Now()
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 3,
			Window:      5 * time.Minute,
		},
	}

	// Record 2 attempts within the window — should not open.
	for i := 0; i < 2; i++ {
		opened := c.Record(now)
		if opened {
			t.Fatalf("circuit opened on attempt %d, want after %d", i+1, c.Budget.MaxAttempts)
		}
	}

	// 3rd attempt should open the circuit (MaxAttempts=3 means 3 attempts open).
	opened := c.Record(now)
	if !opened {
		t.Fatal("circuit did not open after reaching MaxAttempts")
	}
	if c.State != CircuitOpen {
		t.Errorf("state = %v, want open", c.State)
	}
}

func TestCircuitRecordResetsOnWindowExpiry(t *testing.T) {
	now := time.Now()
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 2,
			Window:      time.Minute,
		},
	}

	// 2 attempts within the window → circuit opens.
	c.Record(now)
	opened := c.Record(now)
	if !opened {
		t.Fatal("circuit did not open after 2 attempts within window")
	}

	// Advance time past the window → attempts should be reset.
	future := now.Add(2 * time.Minute)
	c.Record(future) // 1st in new window
	opened = c.Record(future)
	if !opened {
		t.Fatal("circuit did not open after 2 attempts in new window")
	}
}

func TestCircuitOpenBlocksUntilCooldown(t *testing.T) {
	now := time.Now()
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 2,
			Window:      5 * time.Minute,
			Cooldown:    30 * time.Second,
		},
	}

	// Open the circuit.
	c.Record(now)
	c.Record(now)
	if c.State != CircuitOpen {
		t.Fatal("circuit should be open")
	}

	// Blocked: cooldown hasn't elapsed.
	if !c.Blocked(now) {
		t.Error("Blocked(now) = false, want true (still in cooldown)")
	}

	// After cooldown elapses, circuit is no longer blocked.
	afterCooldown := now.Add(31 * time.Second)
	if c.Blocked(afterCooldown) {
		t.Error("Blocked(after cooldown) = true, want false (cooldown elapsed)")
	}
}

func TestCircuitHalfOpenAfterCooldown(t *testing.T) {
	now := time.Now()
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 2,
			Window:      5 * time.Minute,
			Cooldown:    30 * time.Second,
		},
	}

	// Open the circuit.
	c.Record(now)
	c.Record(now)
	if c.State != CircuitOpen {
		t.Fatal("circuit should be open")
	}

	// After cooldown, circuit is half-open.
	afterCooldown := now.Add(31 * time.Second)
	if !c.HalfOpen(afterCooldown) {
		t.Error("HalfOpen(after cooldown) = false, want true")
	}
}

func TestCircuitSuccessClosesOnStableAlive(t *testing.T) {
	now := time.Now()
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 2,
			Window:      5 * time.Minute,
			Cooldown:    30 * time.Second,
		},
		StableAlive: 2, // need 2 consecutive successes
	}

	// Open the circuit.
	c.Record(now)
	c.Record(now)
	if c.State != CircuitOpen {
		t.Fatal("circuit should be open")
	}

	// First success: not yet stable.
	closed := c.RecordSuccess(now)
	if closed {
		t.Fatal("circuit closed on first success, need 2")
	}
	if c.State != CircuitHalfOpen {
		t.Errorf("state = %v, want half-open after first success", c.State)
	}

	// Second success: stable alive → circuit closes.
	closed = c.RecordSuccess(now.Add(time.Second))
	if !closed {
		t.Fatal("circuit did not close on second success")
	}
	if c.State != CircuitClosed {
		t.Errorf("state = %v, want closed", c.State)
	}
}

// --- CircuitStore tests ---

func TestCircuitStoreSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	store := NewCircuitStore(tmp)

	now := time.Now()
	c := &Circuit{
		Key: CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"},
		Budget: CircuitBudget{
			MaxAttempts: 3,
			Window:      5 * time.Minute,
			Cooldown:    30 * time.Second,
		},
		Attempts:    2,
		LastAt:      now,
		State:       CircuitOpen,
		Series:      "series-1",
		StableAlive: 2,
	}

	if err := store.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(c.Key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil")
	}
	if loaded.Key != c.Key {
		t.Errorf("Key = %+v, want %+v", loaded.Key, c.Key)
	}
	if loaded.State != c.State {
		t.Errorf("State = %v, want %v", loaded.State, c.State)
	}
	if loaded.Attempts != c.Attempts {
		t.Errorf("Attempts = %d, want %d", loaded.Attempts, c.Attempts)
	}
	if loaded.Budget.MaxAttempts != c.Budget.MaxAttempts {
		t.Errorf("Budget.MaxAttempts = %d, want %d", loaded.Budget.MaxAttempts, c.Budget.MaxAttempts)
	}
}

func TestCircuitStoreLoadMissing(t *testing.T) {
	tmp := t.TempDir()
	store := NewCircuitStore(tmp)

	c, err := store.Load(CircuitKey{Target: "missing", Input: "none", Signature: "none"})
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if c != nil {
		t.Fatal("Load missing returned non-nil, want nil")
	}
}

func TestCircuitStoreSaveOverwrites(t *testing.T) {
	tmp := t.TempDir()
	store := NewCircuitStore(tmp)
	key := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}

	// Save then update.
	c1 := &Circuit{
		Key:      key,
		Attempts: 1,
		State:    CircuitClosed,
		Budget:   DefaultBudget(),
	}
	if err := store.Save(c1); err != nil {
		t.Fatalf("Save c1: %v", err)
	}

	c2 := &Circuit{
		Key:      key,
		Attempts: 3,
		State:    CircuitOpen,
		Budget:   DefaultBudget(),
	}
	if err := store.Save(c2); err != nil {
		t.Fatalf("Save c2: %v", err)
	}

	loaded, _ := store.Load(key)
	if loaded.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", loaded.Attempts)
	}
	if loaded.State != CircuitOpen {
		t.Errorf("State = %v, want open", loaded.State)
	}
}

func TestCircuitStoreDurableAcrossCalls(t *testing.T) {
	tmp := t.TempDir()
	key := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}

	// Simulate watcher call: save.
	store1 := NewCircuitStore(tmp)
	c := &Circuit{
		Key:      key,
		Attempts: 1,
		State:    CircuitClosed,
		Budget:   DefaultBudget(),
	}
	store1.Save(c)

	// Simulate converge call: load from same store.
	store2 := NewCircuitStore(tmp)
	loaded, _ := store2.Load(key)
	if loaded == nil {
		t.Fatal("circuit not durable across store instances")
	}
	if loaded.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", loaded.Attempts)
	}

	// Manual call: increment.
	loaded.Attempts = 2
	store2.Save(loaded)

	// Verify via third store.
	store3 := NewCircuitStore(tmp)
	loaded2, _ := store3.Load(key)
	if loaded2.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", loaded2.Attempts)
	}
}

func TestDefaultBudget(t *testing.T) {
	b := DefaultBudget()
	if b.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", b.MaxAttempts)
	}
	if b.Window != 5*time.Minute {
		t.Errorf("Window = %v, want 5m", b.Window)
	}
	if b.Cooldown != 30*time.Second {
		t.Errorf("Cooldown = %v, want 30s", b.Cooldown)
	}
}

func TestCircuitStoreFileIsHidden(t *testing.T) {
	tmp := t.TempDir()
	store := NewCircuitStore(tmp)
	key := CircuitKey{Target: "s:p", Input: "task-1", Signature: "sig-a"}

	store.Save(&Circuit{
		Key:    key,
		Budget: DefaultBudget(),
	})

	// The circuit file should be in the state directory with a hidden name.
	stateDir := filepath.Join(tmp, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("reading state dir: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Name() == ".circuit-s:p/task-1/sig-a" {
			found = true
			break
		}
	}
	if !found {
		// The file name is hashed, so just check that something was created.
		for _, e := range entries {
			if len(e.Name()) > 10 && e.Name()[0] == '.' {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("no hidden circuit file found in state directory")
	}
}

// --- CircuitSeries tests ---

func TestCircuitSeriesKey(t *testing.T) {
	// Same target, input → same series.
	s1 := CircuitSeriesKey("s:p", "task-1")
	s2 := CircuitSeriesKey("s:p", "task-1")
	if s1 != s2 {
		t.Fatal("identical series keys differ")
	}
}

func TestCircuitSeriesKeyDiffersOnInput(t *testing.T) {
	s1 := CircuitSeriesKey("s:p", "task-1")
	s2 := CircuitSeriesKey("s:p", "task-2")
	if s1 == s2 {
		t.Fatal("different inputs produce same series key")
	}
}

func TestCircuitSeriesKeyDiffersOnTarget(t *testing.T) {
	s1 := CircuitSeriesKey("s:p1", "task-1")
	s2 := CircuitSeriesKey("s:p2", "task-1")
	if s1 == s2 {
		t.Fatal("different targets produce same series key")
	}
}

// --- Record with Signature tests ---

func TestCircuitSignatureDerivation(t *testing.T) {
	// Same outcome and error → same signature.
	s1 := CircuitSignature("endpoint-dead", "connection refused")
	s2 := CircuitSignature("endpoint-dead", "connection refused")
	if s1 != s2 {
		t.Fatal("identical outcomes produce different signatures")
	}

	// Different outcome → different signature.
	s3 := CircuitSignature("backend-failed", "connection refused")
	if s1 == s3 {
		t.Fatal("different outcomes produce same signature")
	}

	// Different error → different signature.
	s4 := CircuitSignature("endpoint-dead", "timeout")
	if s1 == s4 {
		t.Fatal("different errors produce same signature")
	}
}

func TestCircuitRecordWithSignature(t *testing.T) {
	now := time.Now()
	store := NewCircuitStore(t.TempDir())

	key := CircuitKey{
		Target:    "s:p",
		Input:     "task-1",
		Signature: CircuitSignature("endpoint-dead", "connection refused"),
	}

	// First attempt: circuit not open.
	opened, err := RecordCircuitAttempt(store, key, DefaultBudget(), now)
	if err != nil {
		t.Fatalf("RecordCircuitAttempt: %v", err)
	}
	if opened {
		t.Fatal("circuit opened on first attempt")
	}

	// Second attempt with same signature: still same series.
	opened, err = RecordCircuitAttempt(store, key, DefaultBudget(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("second RecordCircuitAttempt: %v", err)
	}
	if opened {
		t.Fatal("circuit opened on second attempt")
	}

	// Third attempt with same signature: opens (MaxAttempts=3).
	opened, err = RecordCircuitAttempt(store, key, DefaultBudget(), now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("third RecordCircuitAttempt: %v", err)
	}
	if !opened {
		t.Fatal("circuit did not open on third attempt")
	}
}

func TestCircuitRecordChangedSignatureNewSeries(t *testing.T) {
	now := time.Now()
	store := NewCircuitStore(t.TempDir())

	key1 := CircuitKey{
		Target:    "s:p",
		Input:     "task-1",
		Signature: CircuitSignature("endpoint-dead", "err-a"),
	}

	// Open circuit for key1.
	for i := 0; i < 3; i++ {
		RecordCircuitAttempt(store, key1, DefaultBudget(), now)
	}

	// Different signature → new series, should not be open.
	key2 := CircuitKey{
		Target:    "s:p",
		Input:     "task-1",
		Signature: CircuitSignature("endpoint-dead", "err-b"),
	}

	opened, err := RecordCircuitAttempt(store, key2, DefaultBudget(), now)
	if err != nil {
		t.Fatalf("RecordCircuitAttempt for new signature: %v", err)
	}
	if opened {
		t.Fatal("new series with different signature should not open circuit immediately")
	}
}
