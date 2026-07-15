package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

// Fixture: r6 transcript capture of pi ready UI (trimmed to key lines).
// Shows thinking off, checkpoint, model line chrome.
const piReadyCapture = `spec-driven-development, spec-to-code-compliance, srcwalk, stitch-design-taste,
supply-chain-risk-auditor, tasks-axi, tdd, teach, test-driven-development,
to-spec, to-tickets, triage, using-agent-skills, variant-analysis, vuln-report,
wayfinder, wizard, wooyun-legacy, writing-beats, writing-fragments,
writing-great-skills, writing-shape, zeroize-audit

[Extensions]
  @eko24ive/pi-ask:src, @ff-labs/pi-fff@latest:src,
@heyhuynhgiabuu/pi-search@latest:dist, @heyhuynhgiabuu/pi-task:dist,
@juicesharp/rpiv-advisor, @ogulcancelik/pi-herdr,
@sting8k/pi-srcwalk@latest:pi-srcwalk, @sting8k/pi-vcc@latest,
@vigolium/piolium@latest:piolium, herdr-agent-state.ts,
joelhooks/pi-rhizomatic:pi-rhizomatic.ts, pi-augment@latest:src,
pi-boomerang@latest, pi-clinepass-provider:src, pi-hashline-edit-pro,
pi-model-switch@latest, pi-rewind@latest:src, rtk.ts

[Themes]
  piolium-srcery

[Skill conflicts]
  "herdr" collision:
    ✓ auto (user) ~/.pi/agent/skills/herdr/SKILL.md
    ✗ ~/.pi/agent/npm/node_modules/@ogulcancelik/pi-herdr/skills/herdr/SKILL.md
(skipped)


 Advisor restored: zai/glm-5.2, high

────────────────────────────────────────────────────────────────────────────────

────────────────────────────────────────────────────────────────────────────────
~/.treehouse/real-estate-320f76/2/real-estate (detached)
0.0%/256k (auto) • xp                      (cliproxyapi) grok-4.5 • thinking off
◆ 1 checkpoint
`

// Fixture: r6 transcript capture of agy trust prompt.
const agyTrustCapture = `Accessing workspace:

/Users/beowulf/.treehouse/real-estate-320f76/2/real-estate

Do you trust the contents of this project?

Antigravity CLI requires permission to read, edit, and execute files here.

> Yes, I trust this folder
  No, exit

  ↑/↓ Navigate · enter Confirm
                                                    Claude Sonnet 4.6 (Thinking)
`

// Fixture: r7 transcript capture of pi trust prompt.
const piTrustCapture = `Trust project folder?
/Users/beowulf/.treehouse/firstmate-8bf1b0/4/firstmate
→ Trust
  Trust parent folder (...)
  Trust (this session only)
  Do not trust
`

func TestPiReadyPatterns(t *testing.T) {
	patterns := readyPatterns[harness.Pi]
	if len(patterns) == 0 {
		t.Fatal("pi ready patterns should not be empty")
	}

	// Only the patterns that appear in the actual pi capture should match.
	// F6.1 added checkpoint, thinking off, ◆ because old patterns never matched.
	patternsToCheck := []string{"checkpoint", "thinking off", "◆"}
	for _, p := range patternsToCheck {
		if !strings.Contains(piReadyCapture, p) {
			t.Errorf("pi ready pattern %q should match pi capture", p)
		}
	}
}

func TestAgyTrustDetection(t *testing.T) {
	// Verify trust prompt patterns match the actual agy trust capture.
	if !strings.Contains(agyTrustCapture, "Do you trust") {
		t.Error("trust pattern 'Do you trust' should match agy trust capture")
	}
	if !strings.Contains(agyTrustCapture, "Yes, I trust this folder") {
		t.Error("trust pattern 'Yes, I trust this folder' should match agy trust capture")
	}

	// Verify isTrustPrompt detects trust in the agy capture.
	if !isTrustPrompt(agyTrustCapture, harness.Agy) {
		t.Error("isTrustPrompt should detect trust in agy capture")
	}

	// Verify trust is NOT detected in pi capture.
	if isTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("isTrustPrompt should NOT detect trust in pi capture")
	}

	// Verify trust is NOT detected in pi capture when checking agy patterns.
	if isTrustPrompt(piReadyCapture, harness.Agy) {
		t.Error("isTrustPrompt should NOT detect trust in pi capture with agy harness")
	}
}

func TestPiTrustDetection(t *testing.T) {
	// Verify trust prompt patterns match the actual pi trust capture.
	if !strings.Contains(piTrustCapture, "Trust project folder") {
		t.Error("trust pattern 'Trust project folder' should match pi trust capture")
	}
	if !strings.Contains(piTrustCapture, "→ Trust") {
		t.Error("trust pattern '→ Trust' should match pi trust capture")
	}
	if !strings.Contains(piTrustCapture, "Do not trust") {
		t.Error("trust pattern 'Do not trust' should match pi trust capture")
	}

	// Verify isTrustPrompt detects trust in the pi capture.
	if !isTrustPrompt(piTrustCapture, harness.Pi) {
		t.Error("isTrustPrompt should detect trust in pi capture")
	}

	// Verify trust is NOT detected in agy capture when checking pi patterns.
	if isTrustPrompt(agyTrustCapture, harness.Pi) {
		t.Error("isTrustPrompt should NOT detect trust in agy capture with pi harness")
	}

	// Verify trust is NOT detected in pi ready capture (not a trust dialog).
	if isTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("isTrustPrompt should NOT detect trust in pi ready capture")
	}
}

func TestAgyReadyPatterns(t *testing.T) {
	patterns := readyPatterns[harness.Agy]
	if len(patterns) == 0 {
		t.Fatal("agy ready patterns should not be empty")
	}

	// These patterns should NOT match the trust capture (they're ready patterns).
	for _, p := range patterns {
		if strings.Contains(agyTrustCapture, p) {
			t.Errorf("agy ready pattern %q should NOT match trust capture", p)
		}
	}
}

func TestDefaultReadyPatterns(t *testing.T) {
	if len(defaultReadyPatterns) == 0 {
		t.Fatal("defaultReadyPatterns should not be empty")
	}
	patterns := defaultReadyPatterns
	for _, p := range patterns {
		if p == "" {
			t.Error("defaultReadyPatterns should not contain empty patterns")
		}
	}
}
