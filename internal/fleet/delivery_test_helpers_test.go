package fleet

import (
	"os"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func gitEnvForDir(dir string) []string { return append(os.Environ(), "GIT_CEILING_DIRECTORIES="+dir) }
func validIdentity() *domain.DeliveryIdentity {
	return &domain.DeliveryIdentity{Provider: "github", Owner: "minhtri2710", Repo: "munsu", Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42", BaseRef: "main", HeadRef: "feature/test", HeadSHA: "abc123def456abc123def456abc123def456abc1", CapturedAt: "2026-07-18T12:00:00Z"}
}

// newFleetCanonical builds a canonical Task Authority over a fresh real
// temporary home and returns the canonical plus the home directory.
func newFleetCanonical(t *testing.T) (*taskauthority.Canonical, string) {
	t.Helper()
	root := t.TempDir()
	h, err := home.Init(root)
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	return c, root
}

// mustFleetOperation builds a validated Operation from a typed intent.
func mustFleetOperation(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}

// mustFleetTaskID builds a validated task identity.
func mustFleetTaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatalf("NewTaskID(%s): %v", value, err)
	}
	return id
}

// domainOf is domain.Of.
func domainOf(gen, rev uint64) domain.Precondition { return domain.Of(gen, rev) }

// mustFleetCreate creates a queued task through the canonical authority.
func mustFleetCreate(t *testing.T, c *taskauthority.Canonical, taskID string) taskauthority.Outcome {
	t.Helper()
	tid := mustFleetTaskID(t, taskID)
	project, err := domain.NewProjectID("project")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: tid, Owner: "owner", Description: "work",
		Kind: "ship", Project: project, Reason: "create",
	}
	out, err := c.Create(mustFleetOperation(t, "op-create-"+taskID, req), req)
	if err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
	return out
}
