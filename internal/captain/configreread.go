// Package captain implements persistent domain supervisors (captains).
package captain

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/composer"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
)

// ConfigRereadGenName is the file name for config reread generation tracking.
const ConfigRereadGenName = ".config-reread-gen"

// ConfigRereadGenPath returns the path to the config-reread generation file
// under the captain home. The file lives in state/ alongside other tracking
// artifacts (instruction surface nudges, send outbox, etc.).
func ConfigRereadGenPath(captainHome string) string {
	return filepath.Join(captainHome, "state", ConfigRereadGenName)
}

// ConfigPushResult carries the outcome of a config push propagation,
// including whether the inherited surface changed.
type ConfigPushResult struct {
	Changed    bool   // true when inherited config content changed
	Generation int    // generation counter after this push (0 when unchanged)
	OldDigest  string // SHA-256 manifest before push
	NewDigest  string // SHA-256 manifest after push
}

// ComputeInheritedConfigDigest returns a deterministic SHA-256 digest of
// the complete inherited config surface managed by ConfigPush. The digest
// covers all inheritable config files plus general-shared.md and
// projects.md. The result depends only on content, not on timestamps or
// filesystem metadata.
func ComputeInheritedConfigDigest(captainHome string) (string, error) {
	h := sha256.New()
	configDir := filepath.Join(captainHome, "config")
	inheritable := getInheritableList()

	// Collect inheritable config files in sorted order for determinism.
	var names []string
	for _, name := range inheritable {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(configDir, name)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			// Absent inheritable file contributes 0 bytes.
			fmt.Fprintf(h, "config/%s:ABSENT\n", name)
			continue
		}
		if err != nil {
			return "", fmt.Errorf("reading %s for digest: %w", name, err)
		}
		fmt.Fprintf(h, "config/%s:%s\n", name, string(data))
	}

	// general-shared.md
	sharedPath := filepath.Join(captainHome, "data", "general-shared.md")
	data, err := os.ReadFile(sharedPath)
	if os.IsNotExist(err) {
		fmt.Fprintf(h, "data/general-shared.md:ABSENT\n")
	} else if err != nil {
		return "", fmt.Errorf("reading general-shared.md for digest: %w", err)
	} else {
		fmt.Fprintf(h, "data/general-shared.md:%s\n", string(data))
	}

	// projects.md
	projPath := project.RegistryPath(captainHome)
	data, err = os.ReadFile(projPath)
	if os.IsNotExist(err) {
		fmt.Fprintf(h, "data/projects.md:ABSENT\n")
	} else if err != nil {
		return "", fmt.Errorf("reading projects.md for digest: %w", err)
	} else {
		fmt.Fprintf(h, "data/projects.md:%s\n", string(data))
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ReadConfigRereadGen reads the current generation tracking from the
// captain home. Returns (generation, digest, found, error).
// When no file exists, returns (0, "", false, nil).
func ReadConfigRereadGen(captainHome string) (int, string, bool, error) {
	data, err := os.ReadFile(ConfigRereadGenPath(captainHome))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("reading config-reread-gen: %w", err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) < 2 {
		return 0, "", false, fmt.Errorf("config-reread-gen: malformed (expected 2 lines, got %d)", len(lines))
	}
	var gen int
	if _, err := fmt.Sscanf(lines[0], "%d", &gen); err != nil {
		return 0, "", false, fmt.Errorf("config-reread-gen: invalid generation %q: %w", lines[0], err)
	}
	return gen, strings.TrimSpace(lines[1]), true, nil
}

// WriteConfigRereadGen atomically writes the generation tracking file.
func WriteConfigRereadGen(captainHome string, gen int, digest string) error {
	path := ConfigRereadGenPath(captainHome)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir for config-reread-gen: %w", err)
	}
	content := fmt.Sprintf("%d\n%s\n", gen, digest)
	// Use atomicWriteFile for safe replacement.
	return atomicWriteFile(path, []byte(content), 0644)
}

// AdvanceConfigRereadGen computes a digest of the captain's current
// inherited config surface, compares it against the stored generation
// tracking, and advances (writes a new generation) when the digest
// differs. Returns (changed, newGen, oldDigest, newDigest, error).
// The first push always advances (generation 0 → 1).
func AdvanceConfigRereadGen(captainHome string) (bool, int, string, string, error) {
	newDigest, err := ComputeInheritedConfigDigest(captainHome)
	if err != nil {
		return false, 0, "", "", fmt.Errorf("advance config-reread: computing digest: %w", err)
	}

	oldGen, oldDigest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		return false, 0, "", "", fmt.Errorf("advance config-reread: reading gen: %w", err)
	}

	if found && oldDigest == newDigest {
		// No change — generation stays the same.
		return false, oldGen, oldDigest, newDigest, nil
	}

	newGen := oldGen + 1
	if err := WriteConfigRereadGen(captainHome, newGen, newDigest); err != nil {
		return false, 0, oldDigest, newDigest, fmt.Errorf("advance config-reread: writing gen: %w", err)
	}

	return true, newGen, oldDigest, newDigest, nil
}

// ConfigRereadNudgePath returns the path for a pending config-reread
// nudge marker under the captain home. The marker records the generation
// that has been delivered (or is pending delivery) to the captain session.
func ConfigRereadNudgePath(captainHome string) string {
	return filepath.Join(captainHome, "state", ".config-reread-nudge")
}

// WriteConfigRereadNudgeMarker writes a pending notification marker with
// the generation and digest that were delivered. Written before injection,
// cleared only after successful delivery.
func WriteConfigRereadNudgeMarker(captainHome string, gen int, digest string) error {
	path := ConfigRereadNudgePath(captainHome)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir for nudge marker: %w", err)
	}
	content := fmt.Sprintf("gen=%d\ndigest=%s\n", gen, digest)
	return os.WriteFile(path, []byte(content), 0644)
}

// ReadConfigRereadNudgeMarker returns the generation and digest from a
// pending nudge marker. Returns (0, "", false, nil) when no marker exists.
func ReadConfigRereadNudgeMarker(captainHome string) (int, string, bool, error) {
	data, err := os.ReadFile(ConfigRereadNudgePath(captainHome))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("reading nudge marker: %w", err)
	}
	var gen int
	var digest string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok {
			switch strings.TrimSpace(k) {
			case "gen":
				fmt.Sscanf(strings.TrimSpace(v), "%d", &gen)
			case "digest":
				digest = strings.TrimSpace(v)
			}
		}
	}
	if digest == "" {
		return 0, "", false, fmt.Errorf("nudge marker malformed: missing digest")
	}
	return gen, digest, true, nil
}

// RemoveConfigRereadNudgeMarker deletes the pending nudge marker.
func RemoveConfigRereadNudgeMarker(captainHome string) {
	os.Remove(ConfigRereadNudgePath(captainHome))
}

// SentinelMark is the sentinel byte used to mark inject payloads for the
// acknowledged agent-prompt seam. This is the same constant used by the
// AFK injector. The invisible separator character (U+2063) can be embedded
// in any text stream without affecting display.
const SentinelMark = "\u2063"

// QuarantinePath returns the path for quarantined notification artifacts
// under the captain home. Quarantine preserves the payload for inspection
// when post-delivery cleanup fails.
func QuarantinePath(captainHome string, name string) string {
	return filepath.Join(captainHome, "state", ".config-reread-quarantine", name)
}

// QuarantineConfigRereadNudge moves a pending nudge marker into quarantine
// when cleanup fails. Returns the quarantine path or error.
func QuarantineConfigRereadNudge(captainHome string) (string, error) {
	src := ConfigRereadNudgePath(captainHome)
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // nothing to quarantine
		}
		return "", fmt.Errorf("reading nudge marker for quarantine: %w", err)
	}
	qPath := QuarantinePath(captainHome, fmt.Sprintf("pending-%d", os.Getpid()))
	dir := filepath.Dir(qPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating quarantine dir: %w", err)
	}
	if err := os.WriteFile(qPath, data, 0644); err != nil {
		return "", fmt.Errorf("writing quarantine artifact: %w", err)
	}
	// Remove original only after successful quarantine write.
	os.Remove(src)
	return qPath, nil
}

// ConfigRereadInjectMessage builds the literal CONFIG_REREAD: message
// for a given generation and digest.
func ConfigRereadInjectMessage(gen int, digest string) string {
	return fmt.Sprintf("CONFIG_REREAD: generation=%d digest=%.12s", gen, digest)
}

// IsPaneSafeForInject checks whether a terminal pane is safe to inject
// a CONFIG_REREAD message into. Uses the composer package (same logic as
// the AFK injector) to classify the composite state. Returns true only
// when the composer is empty (agent is waiting for input).
func IsPaneSafeForInject(bk session.Backend, windowID string) (bool, string, error) {
	output, err := bk.Capture(windowID, 4)
	if err != nil {
		return false, "", fmt.Errorf("capturing pane %q: %w", windowID, err)
	}

	// Normalise and take the last non-empty line (composer row).
	output = strings.TrimRight(output, "\n\r")
	lines := strings.Split(output, "\n")
	composerLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			composerLine = lines[i]
			break
		}
	}

	// Strip ghost text and ANSI for classification.
	stripped := composer.StripGhost(composerLine)
	plain := strings.TrimSpace(composer.StripANSI(composerLine))
	trimmed := strings.TrimSpace(stripped)

	// Determine bordered status (has box-drawing characters).
	bordered := false
	for _, line := range lines {
		if hasBorderChars(line) {
			bordered = true
			break
		}
	}
	if !bordered && (strings.HasPrefix(plain, "\u276F") || strings.HasPrefix(plain, "\u203A")) {
		bordered = true
	}
	if !bordered && trimmed == "" && plain != "" && !isShellGlyphStart(plain) {
		bordered = true
	}

	verdict := composer.ClassifyContent(trimmed, plain, bordered)
	return verdict == composer.Empty, verdict.String(), nil
}

func hasBorderChars(s string) bool {
	chars := []string{"\u250C", "\u2510", "\u2514", "\u2518", "\u2502",
		"\u256D", "\u256E", "\u2570", "\u256F", "\u251C", "\u2524"}
	for _, c := range chars {
		if strings.Contains(s, c) {
			return true
		}
	}
	return false
}

func isShellGlyphStart(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := string([]rune(s)[0])
	switch first {
	case ">", "$", "%", "#":
		return true
	}
	return false
}

// InjectConfigReread delivers a CONFIG_REREAD message through the
// acknowledged agent-prompt seam. It checks composer safety (only injects
// when the agent is waiting for input), sends the message with the sentinel
// marker, and returns a status string for diagnostic logging.
//
// The inject goes through Backend.SendKeys, but is gated by the composer
// safety check and uses the sentinel marker — this is the acknowledged
// agent-prompt seam, not raw SendKeys for agent turns.
//
// On success, the pending nudge marker is removed. On failure (unsafe,
// endpoint dead), the marker remains for retry. On post-delivery cleanup
// failure, the marker is quarantined so durable generation state is not lost.
func InjectConfigReread(bk session.Backend, windowID string, gen int, digest string) error {
	safe, verdict, err := IsPaneSafeForInject(bk, windowID)
	if err != nil {
		return fmt.Errorf("safety check failed: %w", err)
	}
	if !safe {
		return fmt.Errorf("pane not safe for inject (verdict=%s)", verdict)
	}

	msg := ConfigRereadInjectMessage(gen, digest)
	marked := SentinelMark + msg

	if err := bk.SendKeys(windowID, marked); err != nil {
		return fmt.Errorf("send-keys failed: %w", err)
	}
	return nil
}
