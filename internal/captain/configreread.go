// Package captain implements persistent domain supervisors (captains).
package captain

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// ConfigRereadKey is the mailbox envelope Key for config-reread notifications.
const ConfigRereadKey = "config-reread"

// ConfigRereadGenName is the file name for config reread generation tracking.
const ConfigRereadGenName = ".config-reread-gen"

// ConfigRereadGenPath returns the path to the config-reread generation file
// under the captain home. The file lives in state/ alongside other tracking
// artifacts.
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

// ConfigRereadMessage builds the canonical CONFIG_REREAD message for a
// given generation and full 64-character digest.
func ConfigRereadMessage(gen int, digest string) string {
	return fmt.Sprintf("CONFIG_REREAD: generation=%d digest=%s", gen, digest)
}

// ConfigRereadEnvelopeID returns a deterministic envelope message ID for a
// config-reread requirement. The same (sender, captain, generation, digest)
// always produces the same ID, ensuring idempotent envelope writes.
func ConfigRereadEnvelopeID(senderIdentity, captainIdentity string, generation int, digest string) string {
	data := fmt.Sprintf("config-reread:%s:%s:%d:%s", senderIdentity, captainIdentity, generation, digest)
	h := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", h[:16]) // 32-char hex
}

// EnsureConfigRereadRequirement creates one canonical durable reread
// requirement for the current config generation. It:
//  1. Creates a deterministic mailbox Envelope (General→Captain) with
//     the config-reread message as payload, using ConfigRereadKey.
//  2. Removes any stale config-reread inbox envelopes and pending records
//     (older generations) so only the latest requirement exists.
//  3. Writes the new envelope to the captain's inbox.
//  4. Writes a pending record in the General's outbox.
//  5. Sends the canonical NotificationRef via session.SubmitPrompt (the
//     AgentPrompt seam, never raw SendKeys for agent turns).
//
// On acknowledgment failure (agent not alive, not ready), the pending
// record persists and converge's ReconcileMailboxPending step retries
// the notification on the next cycle. On exact ProcessingAck, converge
// removes the pending.
//
// The parentHome-facing converge/captain_cmd caller handles convergence
// and liveness; this function owns the mailbox write and notification.
func EnsureConfigRereadRequirement(parentHome, captainHome string, gen int, digest string) error {
	// Validate captain provenance and derive identity.
	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("config-reread requirement: %w", err)
	}

	// Derive General sender identity from parent home.
	senderIdentity, senderRank, err := mailbox.ReadHomeIdentity(parentHome)
	if err != nil {
		return fmt.Errorf("config-reread requirement: deriving sender identity: %w", err)
	}

	// Resolve captain task meta for notification delivery (best-effort).
	// Absent meta means the captain is seeded but never launched — we still
	// write the durable envelope/pending, but skip live notification.
	taskID := taskIDForCaptain(captainIdentity)
	meta, metaErr := task.ReadMeta(parentHome, taskID)
	if metaErr != nil {
		fmt.Printf("  %s: no task meta — writing durable requirement only\n", captainIdentity)
	}

	// Build the message marked for General→Captain routing.
	msg := ConfigRereadMessage(gen, digest)
	markedLine := marker.MarkFromGeneral(msg)

	// Create a deterministic envelope.
	env := &mailbox.Envelope{
		MessageID:      ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest),
		SenderRank:     senderRank,
		SenderIdentity: senderIdentity,
		ReceiverRank:   mailbox.RankCaptain,
		ReceiverID:     captainIdentity,
		TaskID:         taskID,
		Key:            ConfigRereadKey,
		Payload:        markedLine,
	}

	canonCaptain, err := canonicalHome(captainHome)
	if err != nil {
		return fmt.Errorf("config-reread requirement: canonicalizing captain home: %w", err)
	}

	receiverStore := mailbox.NewStore(canonCaptain)
	senderStore := mailbox.NewStore(parentHome)

	// Remove stale config-reread inbox/pending records (coalesce).
	if err := removeStaleConfigRereadRecords(canonCaptain, parentHome, senderIdentity, env); err != nil {
		return fmt.Errorf("config-reread requirement: removing stale: %w", err)
	}

	// Write envelope to captain's inbox (idempotent if same content).
	if err := receiverStore.WriteEnvelope(env); err != nil {
		return fmt.Errorf("config-reread requirement: writing inbox envelope: %w", err)
	}

	// Write pending in General's outbox.
	if err := senderStore.WritePending(env); err != nil {
		return fmt.Errorf("config-reread requirement: writing sender pending: %w", err)
	}

	// If no task meta, we're done (durable requirement written, no session to notify).
	if metaErr != nil {
		return nil
	}

	// If meta doesn't have required fields, skip notification.
	if meta["kind"] != "captain" || meta["window"] == "" || meta["sm_id"] != captainIdentity {
		fmt.Printf("  %s: incomplete task meta — requirement written, notification deferred\n", captainIdentity)
		return nil
	}

	windowID := meta["window"]
	bk, _, bkErr := backendForTask(parentHome, meta)
	if bkErr != nil {
		fmt.Printf("  %s: cannot resolve backend — notification deferred: %v\n", captainIdentity, bkErr)
		return nil
	}

	// Send NotificationRef via the AgentPrompt seam.
	ref := mailbox.NotificationRef{
		MessageID:      env.MessageID,
		SenderIdentity: senderIdentity,
	}
	result := session.SubmitPrompt(bk, windowID, ref.Encode())
	if !result.Acknowledged() {
		// Notification not acknowledged — pending remains for converge retry.
		fmt.Printf("  %s: config-reread notification not acknowledged (status=%s)\n", captainIdentity, result.Status)
		return nil // Not an error; converge will retry.
	}

	fmt.Printf("  %s: config-reread gen=%d notified\n", captainIdentity, gen)
	return nil
}

// removeStaleConfigRereadRecords removes any existing config-reread inbox
// envelopes and pending records for older generations, leaving only the
// current one. This is best-effort: stale files that cannot be removed
// (permissions, in-flight I/O) log a diagnostic and do not block.
func removeStaleConfigRereadRecords(captainHome, parentHome, senderIdentity string, current *mailbox.Envelope) error {
	// Clean captain's inbox: remove any config-reread envelope with a
	// different message ID (older generation).
	receiverStore := mailbox.NewStore(captainHome)
	envelopes, err := receiverStore.ListInbox(senderIdentity)
	if err == nil {
		for _, env := range envelopes {
			if env.Key == ConfigRereadKey && env.MessageID != current.MessageID {
				inboxPath := filepath.Join(
					captainHome, "state", mailbox.InboxDir, senderIdentity, env.MessageID+".json",
				)
				os.Remove(inboxPath) // best-effort
				ackPath := filepath.Join(
					captainHome, "state", mailbox.InboxDir, senderIdentity, env.MessageID+".ack",
				)
				os.Remove(ackPath)
			}
		}
	}

	// Clean General's outbox: remove any config-reread pending record with
	// a different message ID (older generation).
	senderStore := mailbox.NewStore(parentHome)
	pending, err := senderStore.ListPending(senderIdentity)
	if err == nil {
		for _, env := range pending {
			if env.Key == ConfigRereadKey && env.MessageID != current.MessageID {
				pendingPath := filepath.Join(
					parentHome, "state", mailbox.OutboxDir, senderIdentity, env.MessageID+".pending",
				)
				os.Remove(pendingPath) // best-effort
			}
		}
	}

	return nil
}

// ReconcileConfigRereadPending reconciles config-reread mailbox records for
// one captain. It checks whether the latest config-reread envelope has been
// acked by the captain, and if so, removes any config-reread pending records
// for older generations.
//
// This is called from converge to ensure config-reread state is clean once
// the captain has acknowledged the latest config.
func ReconcileConfigRereadPending(parentHome string, captainHome string) error {
	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("reconcile config-reread: %w", err)
	}

	senderIdentity, _, err := mailbox.ReadHomeIdentity(parentHome)
	if err != nil {
		return fmt.Errorf("reconcile config-reread: deriving sender identity: %w", err)
	}

	// Read the latest generation.
	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		return fmt.Errorf("reconcile config-reread: reading gen: %w", err)
	}
	if !found {
		return nil // No generation recorded → nothing to reconcile.
	}

	canonCaptain, err := canonicalHome(captainHome)
	if err != nil {
		return fmt.Errorf("reconcile config-reread: canonicalizing: %w", err)
	}

	// Determine the acked envelope ID for the latest generation.
	latestID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)
	captainStore := mailbox.NewStore(canonCaptain)

	// Check if the latest said is acked.
	if !captainStore.IsAcked(senderIdentity, latestID) {
		return nil // Not acked yet — no cleanup.
	}

	// Latest is acked — clean any stale config-reread pending.
	senderStore := mailbox.NewStore(parentHome)
	pending, err := senderStore.ListPending(senderIdentity)
	if err != nil {
		return nil
	}
	for _, env := range pending {
		if env.Key == ConfigRereadKey && env.MessageID != latestID {
			pendingPath := filepath.Join(
				parentHome, "state", mailbox.OutboxDir, senderIdentity, env.MessageID+".pending",
			)
			os.Remove(pendingPath)
		}
	}

	return nil
}

// --- Legacy reconciliation ---

// legacyConfigRereadNudgeName is the old nudge marker filename.
const legacyConfigRereadNudgeName = ".config-reread-nudge"

// legacyConfigRereadNudgePath returns the path to the old nudge marker.
func legacyConfigRereadNudgePath(captainHome string) string {
	return filepath.Join(captainHome, "state", legacyConfigRereadNudgeName)
}

// legacyConfigRereadQuarantineDir returns the old quarantine directory.
func legacyConfigRereadQuarantineDir(captainHome string) string {
	return filepath.Join(captainHome, "state", ".config-reread-quarantine")
}

// ReconcileLegacyConfigReread migrates legacy config-reread artifacts
// (.config-reread-nudge, .config-reread-quarantine) into the canonical
// mailbox-based requirement or removes them if superseded.
//
// Rules:
//   - If a .config-reread-nudge marker exists, parse gen/digest from it,
//     materialize the equivalent mailbox requirement, then delete the marker.
//   - If .config-reread-quarantine exists, scan each file for gen/digest,
//     materialize the latest, then delete the quarantine directory.
//   - Malformed or unparseable records are left in place with a diagnostic
//     error returned (fail-closed). Caller must resolve manually.
//   - If the current generation (from .config-reread-gen) already equals or
//     exceeds what the legacy artifacts describe, just delete the artifacts.
func ReconcileLegacyConfigReread(parentHome, captainHome string) error {
	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("reconcile legacy config-reread: %w", err)
	}

	currentGen, _, genFound, genErr := ReadConfigRereadGen(captainHome)
	if genErr != nil {
		return fmt.Errorf("reconcile legacy config-reread: reading gen: %w", genErr)
	}

	// Try to parse a nudge marker.
	nudgeGen, nudgeDigest, nudgeFound, nudgeErr := readLegacyNudgeMarker(captainHome)
	if nudgeErr != nil {
		return fmt.Errorf("reconcile legacy config-reread: reading nudge: %w", nudgeErr)
	}

	// Try to scan quarantine artifacts.
	quarantineGen, quarantineDigest, quarantineFound, quarantineErr := readLegacyQuarantineLatest(captainHome)
	if quarantineErr != nil {
		return fmt.Errorf("reconcile legacy config-reread: reading quarantine: %w", quarantineErr)
	}

	// If nothing legacy exists, done.
	if !nudgeFound && !quarantineFound {
		return nil
	}

	// Determine the highest legacy generation/digest to materialize.
	legacyGen := 0
	legacyDigest := ""
	if nudgeFound {
		legacyGen = nudgeGen
		legacyDigest = nudgeDigest
	}
	if quarantineFound && quarantineGen > legacyGen {
		legacyGen = quarantineGen
		legacyDigest = quarantineDigest
	}

	// If the current generation already covers or exceeds the legacy,
	// just clean up the legacy artifacts.
	if genFound && currentGen >= legacyGen {
		os.Remove(legacyConfigRereadNudgePath(captainHome))
		os.RemoveAll(legacyConfigRereadQuarantineDir(captainHome))
		fmt.Printf("  %s: legacy config-reread artifacts superseded by gen=%d, cleaned\n", captainIdentity, currentGen)
		return nil
	}

	// Otherwise, materialize the legacy requirement as a mailbox envelope.
	if legacyGen > 0 && legacyDigest != "" {
		fmt.Printf("  %s: migrating legacy config-reread gen=%d to mailbox\n", captainIdentity, legacyGen)
		if err := EnsureConfigRereadRequirement(parentHome, captainHome, legacyGen, legacyDigest); err != nil {
			return fmt.Errorf("reconcile legacy config-reread: materializing requirement: %w", err)
		}
	}

	// Remove legacy artifacts after successful materialisation.
	os.Remove(legacyConfigRereadNudgePath(captainHome))
	os.RemoveAll(legacyConfigRereadQuarantineDir(captainHome))
	fmt.Printf("  %s: legacy config-reread artifacts removed\n", captainIdentity)
	return nil
}

// readLegacyNudgeMarker reads the old two-line .config-reread-nudge marker.
func readLegacyNudgeMarker(captainHome string) (int, string, bool, error) {
	data, err := os.ReadFile(legacyConfigRereadNudgePath(captainHome))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("reading legacy nudge marker: %w", err)
	}

	var gen int
	var digest string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok {
			switch strings.TrimSpace(k) {
			case "gen":
				if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &gen); err != nil {
					return 0, "", false, fmt.Errorf("legacy nudge: invalid gen value %q", v)
				}
			case "digest":
				digest = strings.TrimSpace(v)
			}
		}
	}
	if digest == "" {
		return 0, "", false, fmt.Errorf("legacy nudge marker: malformed — missing digest")
	}
	return gen, digest, true, nil
}

// readLegacyQuarantineLatest scans the quarantine directory and returns the
// highest generation found.
func readLegacyQuarantineLatest(captainHome string) (int, string, bool, error) {
	qDir := legacyConfigRereadQuarantineDir(captainHome)
	entries, err := os.ReadDir(qDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("reading quarantine dir: %w", err)
	}

	bestGen := 0
	bestDigest := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fullPath := filepath.Join(qDir, e.Name())
		data, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			continue
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
		if digest != "" && gen > bestGen {
			bestGen = gen
			bestDigest = digest
		}
	}
	if bestDigest == "" {
		return 0, "", false, nil
	}
	return bestGen, bestDigest, true, nil
}
