package orchestrator

import (
	"os"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestPublishDaemonIdentityUsesCurrentProcess(t *testing.T) {
	h := t.TempDir()
	identity, err := publishDaemonIdentity(h)
	if err != nil {
		t.Skipf("process identity unavailable: %v", err)
	}
	defer clearDaemonIdentity(h, identity)
	got, err := home.ReadWriterIdentity(h, "afk")
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != os.Getpid() || got.StartToken == "" || got.ExecutablePath == "" {
		t.Fatalf("identity=%+v", got)
	}
}
func TestClearDaemonIdentityPreservesNewGeneration(t *testing.T) {
	h := t.TempDir()
	identity, err := publishDaemonIdentity(h)
	if err != nil {
		t.Skipf("process identity unavailable: %v", err)
	}
	newer := identity
	newer.StartToken += "-new"
	if err := home.PublishWriterIdentity(h, "afk", newer); err != nil {
		t.Fatal(err)
	}
	clearDaemonIdentity(h, identity)
	got, err := home.ReadWriterIdentity(h, "afk")
	if err != nil {
		t.Fatal(err)
	}
	if got.StartToken != newer.StartToken {
		t.Fatalf("got=%+v", got)
	}
}
