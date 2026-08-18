package orchestrator

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/home"
)

func publishDaemonIdentity(homeDir string) (home.WriterIdentity, error) {
	executable, startToken, err := processIdentity(os.Getpid())
	if err != nil {
		return home.WriterIdentity{}, fmt.Errorf("reading afk process identity: %w", err)
	}
	canonical, err := home.CanonicalPath(homeDir)
	if err != nil {
		return home.WriterIdentity{}, err
	}
	identity := home.WriterIdentity{SchemaVersion: 1, Kind: "afk", PID: os.Getpid(), StartToken: startToken, ExecutablePath: executable, CanonicalHome: canonical}
	if err := home.PublishWriterIdentity(homeDir, "afk", identity); err != nil {
		return home.WriterIdentity{}, err
	}
	return identity, nil
}

func clearDaemonIdentity(homeDir string, identity home.WriterIdentity) {
	_, _ = home.RemoveWriterIdentityIfMatches(homeDir, "afk", identity)
}

// daemonIdentityForPID returns the published "afk" writer identity when, and
// only when, pid is still the process that published it.
//
// This is the AFK counterpart of ValidatePIDOwnership on the watcher stop path,
// and deliberately the same mechanism rather than a second one: read the
// artifact the daemon published about itself, then ask the kernel about the PID
// and require both the executable path and the start token to match.
//
// It is not state/.lock's second field. That field is time.Now() at the moment
// AcquireLock wrote the file (afk_lock.go), not a process start time, and it is
// formatted RFC3339 while processIdentity returns an opaque per-GOOS token
// (jiffies on linux, sec:usec on darwin, a FILETIME on windows). Comparing the
// two is a category error on every platform, not only on windows -- the lock
// timestamp can say a lock was taken at 10:00 while saying nothing about which
// process now holds that PID. The writer identity carries the token the kernel
// can be re-asked for, so it is the only evidence in the tree that answers "is
// this PID still our daemon".
//
// Every failure is a refusal, never a fallback: a missing artifact, an
// unreadable one, a PID the kernel will not describe, and a genuine mismatch
// all return an error, and the caller must not terminate the PID.
func daemonIdentityForPID(homeDir string, pid int) (home.WriterIdentity, error) {
	identity, err := home.ReadWriterIdentity(homeDir, "afk")
	if err != nil {
		return home.WriterIdentity{}, fmt.Errorf("reading afk writer identity: %w", err)
	}
	if identity.PID != pid {
		return home.WriterIdentity{}, fmt.Errorf("afk writer identity names PID %d, not %d", identity.PID, pid)
	}
	executable, startToken, err := processIdentity(pid)
	if err != nil {
		return home.WriterIdentity{}, fmt.Errorf("reading process identity of PID %d: %w", pid, err)
	}
	if executable != identity.ExecutablePath || startToken != identity.StartToken {
		return home.WriterIdentity{}, fmt.Errorf("PID %d is no longer the process that published the afk identity", pid)
	}
	return identity, nil
}
