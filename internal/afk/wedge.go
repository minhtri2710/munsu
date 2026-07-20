package afk

import (
	"fmt"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

const (
	defaultStaleBeatThreshold = 5 * time.Minute
	defaultWakeCountMax      = 3
)

// WedgeAlarm describes a detected wedge condition that prevents the AFK daemon
// from operating normally.
type WedgeAlarm struct {
	Reason     string    `json:"reason"`
	DetectedAt time.Time `json:"detected_at"`
	BeatAge    string    `json:"beat_age,omitempty"`
	WakeKey    string    `json:"wake_key,omitempty"`
}

// WedgeDetector monitors for wedge conditions:
//   - Stale watcher beat (beat too old beyond threshold)
//   - Missing watcher beat entirely
//   - Repeated identical stale wake (same wake key arriving back-to-back)
//   - Digest stuck (entries accumulated beyond max-defer threshold)
// WedgeDetector monitors for wedge conditions:
//   - Stale watcher beat (beat too old beyond threshold)
//   - Missing watcher beat entirely
//   - Repeated identical stale wake (same wake key arriving back-to-back)
//   - Digest stuck (entries accumulated beyond max-defer threshold)
type WedgeDetector struct {
	mu                 sync.Mutex
	lastWakeKey        string
	lastWakeTime       time.Time
	wakeCount          int
	staleBeatThreshold time.Duration
	wakeCountMax       int
	homeDir            string
}

// NewWedgeDetector creates a new WedgeDetector for the given home directory.
func NewWedgeDetector(homeDir string) *WedgeDetector {
	return &WedgeDetector{
		homeDir:            homeDir,
		staleBeatThreshold: defaultStaleBeatThreshold,
		wakeCountMax:       defaultWakeCountMax,
	}
}

// SetStaleThreshold overrides the default watcher beat staleness threshold.
func (w *WedgeDetector) SetStaleThreshold(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.staleBeatThreshold = d
}

// SetWakeCountMax overrides the default maximum repeated wakes before wedge alarm.
func (w *WedgeDetector) SetWakeCountMax(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wakeCountMax = n
}

// Check evaluates wedge conditions and returns a WedgeAlarm if one is detected.
// Returns nil when no wedge condition exists.
func (w *WedgeDetector) Check(now time.Time) *WedgeAlarm {
	w.mu.Lock()
	staleThreshold := w.staleBeatThreshold
	wakeMax := w.wakeCountMax
	w.mu.Unlock()

	// 1. Check watcher beat staleness.
	beatStatus := lifecycle.ReadBeatStatus(w.homeDir, now)
	if beatStatus.Exists && beatStatus.Stale {
		return &WedgeAlarm{
			Reason:     fmt.Sprintf("watcher beat stale: age=%s threshold=%s", beatStatus.Age.Round(time.Second), staleThreshold.Round(time.Second)),
			DetectedAt: now,
			BeatAge:    beatStatus.Age.Round(time.Second).String(),
		}
	}

	// 2. Check if beat file is missing entirely.
	if !beatStatus.Exists {
		return &WedgeAlarm{
			Reason:     "watcher beat never set",
			DetectedAt: now,
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 3. Check for repeated identical wake (more than wakeCountMax identical wake keys in a row).
	// This is fed externally via FeedWake and reset after a triage cycle.
	if w.wakeCount >= wakeMax {
		alarm := &WedgeAlarm{
			Reason:     fmt.Sprintf("repeated identical stale wake (%d times): key=%s", w.wakeCount, w.lastWakeKey),
			DetectedAt: now,
			WakeKey:    w.lastWakeKey,
		}
		w.wakeCount = 0
		return alarm
	}

	return nil
}

// CheckDigestStuck returns a wedge alarm when the digester has entries that
// have been accumulating beyond the max-defer threshold. This detects stuck
// digests that the General has not resolved.
func (w *WedgeDetector) CheckDigestStuck(firstAt time.Time, maxDefer time.Duration, now time.Time) *WedgeAlarm {
	if firstAt.IsZero() || maxDefer <= 0 {
		return nil
	}
	age := now.Sub(firstAt)
	if age >= maxDefer {
		return &WedgeAlarm{
			Reason:     fmt.Sprintf("digest stuck: age=%s max-defer=%s", age.Round(time.Second), maxDefer.Round(time.Second)),
			DetectedAt: now,
			BeatAge:    age.Round(time.Second).String(),
		}
	}
	return nil
}

// FeedWake tracks a wake key for repetition detection. Call once per triage
// cycle with the most frequent or last wake key seen.
func (w *WedgeDetector) FeedWake(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	// Count as repeat only if same key arrives within 2 poll intervals.
	if key == w.lastWakeKey && !w.lastWakeTime.IsZero() && now.Sub(w.lastWakeTime) < 2*pollInterval {
		w.wakeCount++
	} else {
		w.lastWakeKey = key
		w.wakeCount = 1
	}
	w.lastWakeTime = now
}

// ResetWake resets the wake repetition counter (e.g., after a flush or
// when a different wake is processed).
func (w *WedgeDetector) ResetWake() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wakeCount = 0
	w.lastWakeKey = ""
	w.lastWakeTime = time.Time{}
}
