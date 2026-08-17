package fleet

type fakeArtifactScanner struct{ snapshots [][]WriterArtifact }

func (f *fakeArtifactScanner) Scan(string) ([]WriterArtifact, error) {
	s := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return s, nil
}

type fakeProcessInventory struct{ snapshots [][]WriterProcess }

func (f *fakeProcessInventory) List(string) ([]WriterProcess, error) {
	s := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return s, nil
}

type fakeProcessVerifier struct {
	dead bool
	err  error
}

func (f *fakeProcessVerifier) VerifyDead(WriterArtifact) (bool, error) { return f.dead, f.err }

func process(home string) WriterProcess {
	canonical, err := canonicalHome(home)
	if err != nil {
		panic(err)
	}
	return WriterProcess{PID: 42, StartToken: "123", ExecutablePath: "/bin/munsu", CanonicalHome: canonical, Kind: "watcher", Endpoint: "pane-1", SessionOwner: "session-1"}
}

func artifact(homeDir string) WriterArtifact {
	canonical, err := canonicalHome(homeDir)
	if err != nil {
		panic(err)
	}
	return WriterArtifact{Path: "state/.watcher-identity", Kind: "watcher", PID: 42, StartToken: "123", ExecutablePath: "/bin/munsu", CanonicalHome: canonical, Endpoint: "pane-1", SessionOwner: "session-1"}
}
