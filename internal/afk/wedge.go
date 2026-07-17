package afk

import (
	"fmt"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

const (
	staleBeatThreshold = 5 * time.Minute
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
type WedgeDetector struct {
	mu           sync.Mutex
	lastWakeKey  string
	lastWakeTime time.Time
	wakeCount    int
	homeDir      string
}

// NewWedgeDetector creates a new WedgeDetector for the given home directory.
func NewWedgeDetector(homeDir string) *WedgeDetector {
	return &WedgeDetector{homeDir: homeDir}
}

// Check evaluates wedge conditions and returns a WedgeAlarm if one is detected.
// Returns nil when no wedge condition exists.
func (w *WedgeDetector) Check(now time.Time) *WedgeAlarm {
	// 1. Check watcher beat staleness.
	beatStatus := lifecycle.ReadBeatStatus(w.homeDir, now)
	if beatStatus.Exists && beatStatus.Stale {
		return &WedgeAlarm{
			Reason:     fmt.Sprintf("watcher beat stale: age=%s threshold=%s", beatStatus.Age.Round(time.Second), staleBeatThreshold.Round(time.Second)),
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

	// 3. Check for repeated identical wake (more than 3 identical wake keys in a row).
	// This is fed externally via FeedWake and reset after a triage cycle.
	if w.wakeCount >= 3 {
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
