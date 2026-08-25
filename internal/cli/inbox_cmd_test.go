package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// Actionable captain status (done = general-relevant)
	domainStatusPath, err := home.StatusFilePath(tmpDir, "captain:domain")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(domainStatusPath,
		[]byte("working: processing\n"+
			"done: phase-1 complete\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-actionable captain status (working)
	infraStatusPath, err := home.StatusFilePath(tmpDir, "captain:infra")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(infraStatusPath,
		[]byte("working: healthy\n"), 0644); err != nil {
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
	if !strings.Contains(output, "done: phase-1 complete") {
		t.Errorf("inbox should show done status line, got:\n%s", output)
	}
	if !strings.Contains(output, "!") {
		t.Errorf("inbox should mark actionable captain with !, got:\n%s", output)
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
	if err := home.WriteHomeIdentity(sharedHome, "captain-main", home.RankCaptain); err != nil {
		t.Fatalf("WriteHomeIdentity: %v", err)
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
	if !store.IsAcked("captain-main", env.MessageID) {
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
