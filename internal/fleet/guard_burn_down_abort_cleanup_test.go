//go:build integration

package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Abort releases a cleanup claim, so it must first prove the claim it is
// releasing is the active one for the generation the caller named. A task that
// never retired has no claim at all, which is the shape a delayed abort retry
// arrives in after the claim already went terminal.
func TestAbortRetirementCleanupRefusesWithoutAnActiveClaim(t *testing.T) {
	tmp := t.TempDir()
	auth := canonicalMergeTestAuth(t, tmp, "abort-no-claim")
	taskID := mustTaskID(t, "abort-no-claim")

	// Control: the task is resolvable, so the refusal is attributable to the
	// missing claim rather than to a task Get could not find.
	if _, err := auth.Get(taskID); err != nil {
		t.Fatalf("control Get: %v", err)
	}

	err := AbortRetirementCleanup(auth, tmp, &recordingTeardown{alive: true}, taskID, taskauthority.Generation(1))
	if err == nil {
		t.Fatal("abort with no cleanup claim was accepted, want the not-active refusal")
	}
	if !strings.Contains(err.Error(), "is not active") {
		t.Fatalf("abort error = %v, want the not-active refusal", err)
	}
}
