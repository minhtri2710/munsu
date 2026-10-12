package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestInboxCmd_Wakes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a wake queue with one entry
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)
	wakePath := filepath.Join(tmpDir, "state", ".wake-queue")
	os.WriteFile(wakePath, []byte("1742000000\t1001\tsignal\ttest\tsome status: hello\n"), 0644)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Pending wakes: 1") {
		t.Errorf("inbox should show pending wakes count, got:\n%s", output)
	}
	if !strings.Contains(output, "`munsu wake claim --consumer <id>`") {
		t.Errorf("inbox hint must name the required --consumer flag, got:\n%s", output)
	}
}

// writeClaimedLease writes a lease file the way ClaimWakes lays it out, with
// its mtime set to claimedAt so ClaimState sees a claim that is not fresh.
func writeClaimedLease(t *testing.T, homeDir, leaseID string, expiresAt, claimedAt time.Time, wakeLines ...string) {
	t.Helper()
	if err := os.MkdirAll(home.LeaseDir(homeDir), 0755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("%s\tmunsu:herdr\t%d\t%d\n%s\n", leaseID, expiresAt.Unix(), claimedAt.Unix(), strings.Join(wakeLines, "\n"))
	path := home.LeaseFilePath(homeDir, leaseID)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, claimedAt, claimedAt); err != nil {
		t.Fatal(err)
	}
}

func runInboxView(t *testing.T, homeDir string) string {
	t.Helper()
	t.Setenv("MUNSU_HOME", homeDir)
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"inbox"})
	if err := root.Execute(); err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}
	return buf.String()
}

// A wake held in a lease is in neither queue, so the inbox is the only place
// the General can see it: it must not read "none pending" and must carry the
// derived state and its evidence.
func TestInboxCmd_ClaimedWakes(t *testing.T) {
	homeDir := t.TempDir()
	claimedAt := time.Now().Add(-10 * time.Minute)
	writeClaimedLease(t, homeDir, "lease-1", time.Now().Add(time.Hour), claimedAt,
		fmt.Sprintf("%d\t7\tuplink\ttask-1\tdone: PR merged", claimedAt.Unix()))

	output := runInboxView(t, homeDir)

	if strings.Contains(output, "none pending") || strings.Contains(output, "All quiet") {
		t.Errorf("a claimed wake must not read as none pending or all quiet, got:\n%s", output)
	}
	for _, want := range []string{
		"Wakes: none queued, 1 claimed",
		"Claimed wakes: 1",
		"lease-1 held by munsu:herdr, claimed 10m",
		fmt.Sprintf(`%d:7 uplink task-1 "done: PR merged" [stalled]: claimed 10m`, claimedAt.Unix()),
		"by munsu:herdr, no ack or resolve since",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("inbox missing %q, got:\n%s", want, output)
		}
	}
}

// An unreadable lease directory is unknown, never none.
func TestInboxCmd_UnreadableLeaseDirectoryIsUnknown(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.LeaseDir(homeDir), []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	output := runInboxView(t, homeDir)

	if strings.Contains(output, "none pending") || strings.Contains(output, "All quiet") {
		t.Errorf("unreadable leases must not read as none pending or all quiet, got:\n%s", output)
	}
	if !strings.Contains(output, "Claimed wakes: unknown") {
		t.Errorf("inbox should show claimed wakes as unknown, got:\n%s", output)
	}
}

func TestInboxCmd_NoWakes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "state"), 0755)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Wakes: none pending") {
		t.Errorf("inbox should show no wakes, got:\n%s", output)
	}
}

func TestInboxCmd_CaptainStatus(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create captain status files
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Actionable captain status (blocked = general-relevant)
	domainStatusPath, err := home.StatusFilePath(tmpDir, "captain:domain")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(domainStatusPath,
		[]byte("working: processing\n"+
			"blocked: phase-1 needs a decision\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-actionable captain status (done is audit-only, not general-relevant)
	infraStatusPath, err := home.StatusFilePath(tmpDir, "captain:infra")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(infraStatusPath,
		[]byte("done: healthy\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err = root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "domain") {
		t.Errorf("inbox should show captain:domain status, got:\n%s", output)
	}
	if !strings.Contains(output, "infra") {
		t.Errorf("inbox should show captain:infra status, got:\n%s", output)
	}
	if !strings.Contains(output, "blocked: phase-1 needs a decision") {
		t.Errorf("inbox should show blocked status line, got:\n%s", output)
	}
	if !strings.Contains(output, "! domain: blocked:") {
		t.Errorf("inbox should mark the blocked captain with !, got:\n%s", output)
	}
	if !strings.Contains(output, "  infra: done: healthy") {
		t.Errorf("inbox must not mark a done captain (audit-only), got:\n%s", output)
	}
}

func TestInboxCmd_EmptyState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "state"), 0755)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No captains registered.") {
		t.Errorf("inbox should show 'No captains registered.', got:\n%s", output)
	}
}

// The soldier is the receiver of every captain-to-soldier envelope, and
// `munsu inbox ack` is the only thing that writes the ack the captain's queue
// reconciles against. A soldier runs with MUNSU_HOME set to its dispatcher's
// home, so the command must identify it by its task, not by that home.
func TestInboxAck_SoldierIdentifiedByItsTask(t *testing.T) {
	sharedHome := filepath.Join(t.TempDir(), "captain-main")
	if err := os.MkdirAll(sharedHome, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := home.SeedCaptainProvenance(sharedHome, "captain-main"); err != nil {
		t.Fatalf("SeedCaptainProvenance: %v", err)
	}
	const taskID = "task:soldier-1"
	if err := home.WriteMeta(sharedHome, taskID, map[string]string{"window": "w"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	env := &home.Envelope{
		SenderRank:     home.RankCaptain,
		SenderIdentity: "captain-main",
		ReceiverRank:   home.RankSoldier,
		ReceiverID:     home.ReceiverIDForTask(taskID),
		TaskID:         taskID,
		Payload:        "do: the work",
	}
	store := home.NewStore(sharedHome)
	if err := store.WriteEnvelope(env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	ref := home.NotificationRef{MessageID: env.MessageID, SenderIdentity: "captain-main"}

	t.Setenv("MUNSU_HOME", sharedHome)
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_TASK_ID", taskID)

	runInbox := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		root := NewRootCommand()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs(args)
		err := root.Execute()
		return buf.String(), err
	}

	out, err := runInbox(t, "inbox", "receive", ref.Encode())
	if err != nil {
		t.Fatalf("inbox receive: %v (%s)", err, out)
	}
	if !strings.Contains(out, "do: the work") {
		t.Fatalf("inbox receive did not return the payload, got:\n%s", out)
	}

	if out, err := runInbox(t, "inbox", "ack", ref.Encode()); err != nil {
		t.Fatalf("inbox ack: %v (%s)", err, out)
	}
	if !store.IsAcked(env) {
		t.Fatal("inbox ack wrote no ack the captain queue can reconcile against")
	}

	// A soldier is identified by its task. Without one there is no receiver to
	// construct, and the command must refuse rather than fall back to the
	// identity of the home it is borrowing.
	t.Setenv("MUNSU_TASK_ID", "")
	if out, err := runInbox(t, "inbox", "ack", ref.Encode()); err == nil {
		t.Fatalf("inbox ack accepted a soldier with no task, got:\n%s", out)
	} else if !strings.Contains(err.Error(), "MUNSU_TASK_ID") {
		t.Fatalf("error = %v, want the missing-task refusal", err)
	}
}

func TestReadLastLineTakesALineOverTheScannerLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.status")
	long := "done: " + strings.Repeat("x", 100*1024)
	if err := os.WriteFile(path, []byte("working: a\n"+long+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readLastLine(path); got != long {
		t.Fatalf("readLastLine() returned %d bytes, want the %d-byte last line", len(got), len(long))
	}
}
