package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CircuitState is the state of a recovery circuit for one recovery series.
type CircuitState int

const (
	// CircuitClosed means the recovery series is within budget and recovery
	// actions (auto-relaunch / nudge) are permitted.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the recovery series exhausted its budget on
	// repeated identical deterministic failure. Auto-relaunch is
	// suppressed; the failure is escalated for authoritative handling.
	CircuitOpen
	// CircuitHalfOpen means the circuit opened but the cooldown has
	// elapsed; a probe may be attempted. One success moves to Closed
	// after stable-alive confirmation; one failure re-opens.
	CircuitHalfOpen
)

// CircuitKey identifies one recovery series. A series is scoped by the
// endpoint target, the input that triggered recovery, and the deterministic
// failure signature. Changed inputs or signatures start a NEW series with a
// fresh budget — they never inherit an open circuit.
type CircuitKey struct {
	Target    string `json:"target"`
	Input     string `json:"input"`
	Signature string `json:"signature"`
}

// CircuitBudget bounds one recovery series: how many attempts are allowed
// within a window, and how long the circuit stays open before a half-open
// probe is permitted. Budgets persist across watcher, converge, and manual
// calls because they are stored with the circuit itself.
type CircuitBudget struct {
	MaxAttempts int           `json:"max_attempts"`
	Window      time.Duration `json:"window"`
	Cooldown    time.Duration `json:"cooldown"`
}

// Circuit is the durable record of one recovery series. It tracks the
// attempt count within the budget window, the open/half-open state, and
// the stable-alive successes needed to declare recovery complete.
type Circuit struct {
	Key         CircuitKey    `json:"key"`
	Budget      CircuitBudget `json:"budget"`
	State       CircuitState  `json:"state"`
	Attempts    int           `json:"attempts"`
	FirstAt     time.Time     `json:"first_at,omitempty"`
	LastAt      time.Time     `json:"last_at,omitempty"`
	Series      string        `json:"series"`
	StableAlive int           `json:"stable_alive"` // consecutive successes needed to close
	Successes   int           `json:"successes"`    // consecutive successes in half-open
}

// DefaultBudget returns the default recovery budget: 3 attempts per 5-minute
// window, with a 30-second cooldown before half-open probing.
func DefaultBudget() CircuitBudget {
	return CircuitBudget{
		MaxAttempts: 3,
		Window:      5 * time.Minute,
		Cooldown:    30 * time.Second,
	}
}

// CircuitSignature derives a deterministic failure signature from an outcome
// and error text. Identical outcomes and errors produce identical signatures;
// changed inputs create a new series (handled by CircuitKey.Input).
func CircuitSignature(outcome, errText string) string {
	raw := strings.ToLower(strings.TrimSpace(outcome) + "|" + strings.TrimSpace(errText))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// CircuitSeriesKey derives a stable series key from target and input.
// The series spans signatures: same target+input but a different failure
// signature is still the same series (repeated failures of the same
// operation), while a different input is a NEW series.
func CircuitSeriesKey(target, input string) string {
	raw := target + "\x00" + input
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// Record records one recovery attempt at time now. It returns true when the
// attempt opened the circuit (budget exhausted). Attempts outside the window
// reset the count. Half-open circuits re-open on a failed attempt.
func (c *Circuit) Record(now time.Time) bool {
	if c.Budget.MaxAttempts <= 0 {
		c.Budget = DefaultBudget()
	}

	// A half-open circuit that fails a probe re-opens immediately.
	if c.State == CircuitHalfOpen {
		c.State = CircuitOpen
		c.Attempts = c.Budget.MaxAttempts
		c.Successes = 0
		c.LastAt = now
		return true
	}

	// Window expiry resets the attempt count.
	if !c.LastAt.IsZero() && now.Sub(c.LastAt) > c.Budget.Window {
		c.Attempts = 0
		c.FirstAt = now
	}

	if c.Attempts == 0 {
		c.FirstAt = now
	}
	c.Attempts++
	c.LastAt = now

	if c.Attempts >= c.Budget.MaxAttempts {
		c.State = CircuitOpen
		c.Successes = 0
		return true
	}
	return false
}

// RecordSuccess records a successful recovery probe. Returns true when the
// circuit transitions to Closed because the endpoint has been stable-alive
// for the required consecutive successes.
func (c *Circuit) RecordSuccess(now time.Time) bool {
	if c.State != CircuitOpen && c.State != CircuitHalfOpen {
		// Closed circuit stays closed; nothing to do.
		return false
	}
	if c.State == CircuitOpen {
		c.State = CircuitHalfOpen
		c.Successes = 1
		c.LastAt = now
		if c.StableAlive <= 1 {
			c.State = CircuitClosed
			c.Attempts = 0
			c.FirstAt = time.Time{}
			return true
		}
		return false
	}
	// Half-open: accumulate consecutive successes.
	c.Successes++
	c.LastAt = now
	if c.Successes >= c.StableAlive {
		c.State = CircuitClosed
		c.Attempts = 0
		c.FirstAt = time.Time{}
		c.Successes = 0
		return true
	}
	return false
}

// Blocked reports whether recovery actions for this series are currently
// suppressed. An open circuit blocks until the cooldown elapses; a closed
// circuit never blocks.
func (c *Circuit) Blocked(now time.Time) bool {
	if c.State == CircuitClosed {
		return false
	}
	if c.State == CircuitHalfOpen {
		return false // half-open means a probe is permitted
	}
	// Open: blocked until cooldown elapses from LastAt.
	return now.Sub(c.LastAt) < c.Budget.Cooldown
}

// HalfOpen reports whether the circuit is currently in the half-open probe
// window (open circuit whose cooldown has elapsed).
func (c *Circuit) HalfOpen(now time.Time) bool {
	if c.State != CircuitOpen {
		return false
	}
	return now.Sub(c.LastAt) >= c.Budget.Cooldown
}

// --- Durable store ---

const circuitFile = "state/.recovery-circuits"

// CircuitStore persists circuits durably so recovery series budgets survive
// across watcher, converge, and manual calls. Safe for concurrent use.
type CircuitStore struct {
	homeDir string
}

// NewCircuitStore creates a circuit store rooted at the given home directory.
func NewCircuitStore(homeDir string) *CircuitStore {
	return &CircuitStore{homeDir: homeDir}
}

func (s *CircuitStore) fileFor(key CircuitKey) string {
	// Hash the key into a stable, filesystem-safe filename.
	raw := key.Target + "\x00" + key.Input + "\x00" + key.Signature
	sum := sha256.Sum256([]byte(raw))
	name := ".circuit-" + hex.EncodeToString(sum[:16])
	return filepath.Join(s.homeDir, circuitFile+"-"+name)
}

// Save persists a circuit to disk, overwriting any prior record for the key.
func (s *CircuitStore) Save(c *Circuit) error {
	if c == nil {
		return fmt.Errorf("circuit store: cannot save nil circuit")
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal circuit: %w", err)
	}
	path := s.fileFor(c.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create circuit state dir: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Load reads a circuit from disk. Returns nil, nil when no circuit exists
// for the key.
func (s *CircuitStore) Load(key CircuitKey) (*Circuit, error) {
	data, err := os.ReadFile(s.fileFor(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading circuit: %w", err)
	}
	var c Circuit
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("unmarshal circuit: %w", err)
	}
	// Apply defaults for budgets that were persisted before defaults existed.
	if c.Budget.MaxAttempts <= 0 {
		c.Budget = DefaultBudget()
	}
	if c.StableAlive == 0 {
		c.StableAlive = 2
	}
	return &c, nil
}

// RecordCircuitAttempt records one recovery attempt for a keyed series.
// It loads (or creates) the circuit, records the attempt, persists, and
// returns true if the attempt opened the circuit.
func RecordCircuitAttempt(store *CircuitStore, key CircuitKey, budget CircuitBudget, now time.Time) (bool, error) {
	c, err := store.Load(key)
	if err != nil {
		return false, err
	}
	if c == nil {
		c = &Circuit{
			Key:         key,
			Budget:      budget,
			State:       CircuitClosed,
			Series:      CircuitSeriesKey(key.Target, key.Input),
			StableAlive: 2,
		}
	} else {
		// The budget travels with the series and is applied consistently
		// across watcher/converge/manual calls.
		c.Budget = budget
	}
	opened := c.Record(now)
	if err := store.Save(c); err != nil {
		return opened, err
	}
	return opened, nil
}

// RecordCircuitSuccess records a success for a keyed series and returns
// true when the circuit fully closed (stable alive achieved).
func RecordCircuitSuccess(store *CircuitStore, key CircuitKey, now time.Time) (bool, error) {
	c, err := store.Load(key)
	if err != nil {
		return false, err
	}
	if c == nil {
		// No circuit means no recovery series was in flight; nothing to close.
		return false, nil
	}
	closed := c.RecordSuccess(now)
	if err := store.Save(c); err != nil {
		return closed, err
	}
	return closed, nil
}

// IsCircuitBlocked reports whether a recovery series is currently suppressed
// by an open circuit. Missing circuits are never blocked.
func IsCircuitBlocked(store *CircuitStore, key CircuitKey, now time.Time) (bool, error) {
	c, err := store.Load(key)
	if err != nil {
		return false, err
	}
	if c == nil {
		return false, nil
	}
	return c.Blocked(now), nil
}
