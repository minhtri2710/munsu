package fleet

type IntegrationStatus struct {
	Harness string
	Scope   string
	State   string
	Message string
}

type IntegrationPort interface {
	EnsureCaptain(home, harness string) error
	Status(home, harness string) (IntegrationStatus, error)
	// CaptainPaths returns the home-relative, slash-separated paths that
	// EnsureCaptain writes for harness.
	CaptainPaths(home, harness string) ([]string, error)
}
