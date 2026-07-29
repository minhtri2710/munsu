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
