package fence

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	sandboxExec = "/usr/bin/sandbox-exec"
	touchBin    = "/usr/bin/touch"
)

// Wrap returns the fenced argv for a launch argv: sandbox-exec with the exact
// profile, then the program. The inline profile keeps the launch command
// self-contained, so the probed profile and the launched profile are one string.
func (f *Fence) Wrap(argv []string) ([]string, error) {
	if len(argv) == 0 || argv[0] == "" || strings.HasPrefix(argv[0], "-") {
		return nil, fmt.Errorf("fence: cannot wrap argv %q", argv)
	}
	return append([]string{sandboxExec, "-p", f.profile}, argv...), nil
}

// Probe runs, under the exact profile Wrap uses, a write to an allowed root
// (the control) and then one write to each directory the profile must refuse:
// the primary checkout, the git common dir, and for a reviewer the checkout
// under review and its git dir. It returns a nil error only when the control
// succeeds and every refused write fails with "Operation not permitted". A
// write that succeeds, a missing sandbox-exec, a profile that does not compile
// and any other error are failures. The Evidence is returned either way.
func (f *Fence) Probe(ctx context.Context) (Evidence, error) {
	ev := Evidence{Role: f.role, Harness: f.harness, ProfileDigest: f.digest}
	fail := func(s ProbeStep) (Evidence, error) {
		ev.Steps = append(ev.Steps, s)
		return ev, fmt.Errorf("fence probe: %s: %s %s", s.Outcome, strings.Join(s.Command, " "), s.Detail)
	}
	if _, err := os.Stat(sandboxExec); err != nil {
		return fail(ProbeStep{Command: []string{sandboxExec}, Outcome: OutcomeUnavailable, Detail: err.Error()})
	}

	// The control proves the sandbox ran and the profile compiled, so a later
	// refusal is the profile's and not a broken launcher's.
	ctl, err := os.MkdirTemp("", "fence-control-")
	if err != nil {
		return fail(ProbeStep{Outcome: OutcomeError, Detail: err.Error()})
	}
	defer os.RemoveAll(ctl)
	cmd := f.touchCommand(filepath.Join(ctl, "control"))
	out, runErr := run(ctx, cmd)
	if _, statErr := os.Stat(filepath.Join(ctl, "control")); runErr != nil || statErr != nil {
		return fail(ProbeStep{Command: f.record(cmd), Outcome: OutcomeControlFailed, Detail: detail(out, runErr)})
	}
	ev.Steps = append(ev.Steps, ProbeStep{Command: f.record(cmd), Outcome: OutcomeAllowed})

	for _, dir := range f.refuse {
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return fail(ProbeStep{Outcome: OutcomeError, Detail: err.Error()})
		}
		target := filepath.Join(dir, ".fence-probe-"+hex.EncodeToString(nonce[:]))
		cmd := f.touchCommand(target)
		out, runErr := run(ctx, cmd)
		if _, statErr := os.Lstat(target); statErr == nil {
			os.Remove(target)
			return fail(ProbeStep{Command: f.record(cmd), Outcome: OutcomeWritten, Detail: detail(out, runErr)})
		}
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || !bytes.Contains(out, []byte("Operation not permitted")) {
			return fail(ProbeStep{Command: f.record(cmd), Outcome: OutcomeError, Detail: detail(out, runErr)})
		}
		ev.Steps = append(ev.Steps, ProbeStep{Command: f.record(cmd), Outcome: OutcomeRefused})
	}
	return ev, nil
}

func (f *Fence) touchCommand(target string) []string {
	return []string{sandboxExec, "-p", f.profile, touchBin, target}
}

// record is cmd with the profile text replaced by its digest, so the evidence
// names the exact profile without carrying it.
func (f *Fence) record(cmd []string) []string {
	out := append([]string{}, cmd...)
	out[2] = "profile-sha256:" + f.digest
	return out
}

func run(ctx context.Context, argv []string) ([]byte, error) {
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func detail(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if err != nil {
		s += " (" + err.Error() + ")"
	}
	return s
}
